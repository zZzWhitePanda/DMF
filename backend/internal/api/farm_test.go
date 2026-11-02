package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
)

const testCookie = "dmf_test"

var testChoices = []string{"Grinder", "Fisher", "Hunter"}

type farmServer struct {
	t       *testing.T
	db      *db.DB
	handler http.Handler
	manager *runtime.Manager
	account int64
}

// newFarmServer mounts the farm routes behind the same middleware as
// cmd/server, with a licensed user signed in as session "licensed".
func newFarmServer(t *testing.T) *farmServer {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	s := &farmServer{t: t, db: d, manager: runtime.New()}
	s.addUser("licensed")
	if err := d.MintLicense(ctx, "key", "licensed", "", nil); err != nil {
		t.Fatal(err)
	}
	s.account, err = d.CreateAccount(ctx, db.DiscordAccount{GoogleID: "licensed", Name: "my farm", ConfigYAML: defaultConfigYAML})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	NewFarmHandlers(d, s.manager).Register(mux)
	NewWSHandlers(d, s.manager, nil).Register(mux)
	mw := auth.NewMiddleware(auth.NewGoogleProvider(auth.Config{SessionCookie: testCookie}), d)
	s.handler = mw.RequireAuth(mw.RequireLicensed(mux))
	return s
}

// addUser creates a user with a live session named after them.
func (s *farmServer) addUser(id string) {
	s.t.Helper()
	ctx := context.Background()
	if err := s.db.UpsertUser(ctx, db.User{GoogleID: id, Email: id + "@example.com"}); err != nil {
		s.t.Fatal(err)
	}
	now := time.Now()
	sess := db.Session{ID: id, GoogleID: id, HWID: "hwid", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.db.CreateSession(ctx, sess); err != nil {
		s.t.Fatal(err)
	}
}

func (s *farmServer) do(session, method, path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if session != "" {
		r.AddCookie(&http.Cookie{Name: testCookie, Value: session})
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w
}

func (s *farmServer) post(body string) int {
	s.t.Helper()
	return s.do("licensed", http.MethodPost, "/api/farm/flow-choice", body).Code
}

// start runs the farm with no clusters, so tests drive its bus themselves.
func (s *farmServer) start() *events.Bus {
	s.t.Helper()
	if err := s.manager.Start(s.account, &config.Config{}); err != nil {
		s.t.Fatal(err)
	}
	return s.manager.Bus(s.account)
}

// summaryPicker reads flow_picker from GET /api/farm.
func (s *farmServer) summaryPicker() *events.FlowPicker {
	s.t.Helper()
	var body struct {
		FlowPicker *events.FlowPicker `json:"flow_picker"`
	}
	w := s.do("licensed", http.MethodGet, "/api/farm", "")
	if w.Code != http.StatusOK {
		s.t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		s.t.Fatal(err)
	}
	return body.FlowPicker
}

// ask blocks a goroutine on a picker the way commands.Flow does.
func ask(t *testing.T, bus *events.Bus) <-chan int {
	t.Helper()
	answer := make(chan int, 1)
	go func() {
		idx, err := bus.AskFlowChoice(context.Background(), "Your flows", testChoices)
		if err != nil {
			t.Errorf("AskFlowChoice: %v", err)
		}
		answer <- idx
	}()
	deadline := time.Now().Add(time.Second)
	for bus.FlowPicker() == nil {
		if time.Now().After(deadline) {
			t.Fatal("picker never opened")
		}
		time.Sleep(time.Millisecond)
	}
	return answer
}

func unblocked(t *testing.T, answer <-chan int) int {
	t.Helper()
	select {
	case idx := <-answer:
		return idx
	case <-time.After(time.Second):
		t.Fatal("grinder is still blocked")
		return 0
	}
}

func stillBlocked(t *testing.T, answer <-chan int) {
	t.Helper()
	select {
	case idx := <-answer:
		t.Fatalf("grinder was unblocked with %d", idx)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFlowChoiceUnblocksTheGrinder(t *testing.T) {
	s := newFarmServer(t)
	answer := ask(t, s.start())

	if got := s.summaryPicker(); got == nil || got.Message != "Your flows" || len(got.Choices) != 3 {
		t.Fatalf("summary picker = %+v", got)
	}
	if code := s.post(`{"index":1}`); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := unblocked(t, answer); got != 1 {
		t.Fatalf("grinder got %d, want 1", got)
	}
	if got := s.summaryPicker(); got != nil {
		t.Fatalf("picker still reported after the answer: %+v", got)
	}
}

func TestFlowChoiceMinusOneCancels(t *testing.T) {
	s := newFarmServer(t)
	answer := ask(t, s.start())

	if code := s.post(`{"index":-1}`); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := unblocked(t, answer); got != -1 {
		t.Fatalf("grinder got %d, want -1", got)
	}
}

func TestFlowChoiceDoubleClick(t *testing.T) {
	s := newFarmServer(t)
	bus := s.start()
	answer := ask(t, bus)

	if code := s.post(`{"index":2}`); code != http.StatusOK {
		t.Fatalf("first click: status %d", code)
	}
	unblocked(t, answer)
	if code := s.post(`{"index":2}`); code != http.StatusConflict {
		t.Fatalf("second click: status %d, want 409", code)
	}

	// The second click must not be waiting for the next picker.
	next := ask(t, bus)
	stillBlocked(t, next)
	if code := s.post(`{"index":0}`); code != http.StatusOK {
		t.Fatalf("next picker: status %d", code)
	}
	if got := unblocked(t, next); got != 0 {
		t.Fatalf("next picker got %d, want 0", got)
	}
}

func TestFlowChoiceConcurrentClicksCountOnce(t *testing.T) {
	s := newFarmServer(t)
	answer := ask(t, s.start())

	codes := make([]int, 8)
	var wg sync.WaitGroup
	for n := range codes {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/api/farm/flow-choice", strings.NewReader(`{"index":0}`))
			r.AddCookie(&http.Cookie{Name: testCookie, Value: "licensed"})
			w := httptest.NewRecorder()
			s.handler.ServeHTTP(w, r)
			codes[n] = w.Code
		}(n)
	}
	wg.Wait()

	accepted := 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			accepted++
		case http.StatusConflict:
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d clicks accepted, want 1", accepted)
	}
	unblocked(t, answer)
}

func TestFlowChoiceStaleAnswerIsRejected(t *testing.T) {
	s := newFarmServer(t)
	bus := s.start()

	if code := s.post(`{"index":1}`); code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	answer := ask(t, bus)
	stillBlocked(t, answer)
}

func TestFlowChoiceFarmNotRunning(t *testing.T) {
	s := newFarmServer(t)
	w := s.do("licensed", http.MethodPost, "/api/farm/flow-choice", `{"index":0}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "farm not running") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestFlowChoiceBadBodies(t *testing.T) {
	s := newFarmServer(t)
	answer := ask(t, s.start())

	for _, body := range []string{
		``,
		`not json`,
		`{}`,
		`{"index":null}`,
		`{"index":"1"}`,
		`{"index":1.5}`,
		`{"choice":1}`,
		`{"index":-2}`,
		`{"index":3}`,
	} {
		if code := s.post(body); code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, code)
		}
	}
	stillBlocked(t, answer)

	if code := s.post(`{"index":0}`); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	unblocked(t, answer)
}

func TestFlowChoiceOnlyTakesPost(t *testing.T) {
	s := newFarmServer(t)
	s.start()
	w := s.do("licensed", http.MethodGet, "/api/farm/flow-choice", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", w.Code)
	}
}

func TestFlowChoiceRequiresALicense(t *testing.T) {
	s := newFarmServer(t)
	answer := ask(t, s.start())
	s.addUser("unlicensed")

	if w := s.do("", http.MethodPost, "/api/farm/flow-choice", `{"index":0}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: status %d, want 401", w.Code)
	}
	if w := s.do("unlicensed", http.MethodPost, "/api/farm/flow-choice", `{"index":0}`); w.Code != http.StatusForbidden {
		t.Fatalf("unlicensed: status %d, want 403", w.Code)
	}
	stillBlocked(t, answer)
	s.post(`{"index":0}`)
	unblocked(t, answer)
}

func (s *farmServer) summaryTokenRejected() bool {
	s.t.Helper()
	var body struct {
		TokenRejected bool `json:"token_rejected"`
	}
	w := s.do("licensed", http.MethodGet, "/api/farm", "")
	if w.Code != http.StatusOK {
		s.t.Fatalf("summary: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		s.t.Fatal(err)
	}
	return body.TokenRejected
}

// A tab opened after the one-shot event must still learn the token is bad,
// and a new run must not inherit the old run's rejection.
func TestSummaryReportsRejectedTokenUntilNextRun(t *testing.T) {
	s := newFarmServer(t)
	if s.summaryTokenRejected() {
		t.Fatal("reported rejected with no farm running")
	}
	bus := s.start()
	if s.summaryTokenRejected() {
		t.Fatal("reported rejected before Discord refused anything")
	}
	bus.Send(events.Event{Kind: events.GatewayDown, Message: "connection reset"})
	if s.summaryTokenRejected() {
		t.Fatal("an ordinary disconnect counted as a rejected token")
	}
	bus.Send(events.Event{Kind: events.GatewayDown, Outcome: events.OutcomeAuthFailed})
	if !s.summaryTokenRejected() {
		t.Fatal("rejected token not reported")
	}
	s.manager.Stop(s.account)
	if s.summaryTokenRejected() {
		t.Fatal("rejection outlived the run")
	}
	s.start()
	if s.summaryTokenRejected() {
		t.Fatal("new run inherited the old rejection")
	}
}

// summarySession reads session from GET /api/farm, nil if there isn't one.
func (s *farmServer) summarySession() *struct {
	Earned  int   `json:"earned"`
	EndedAt int64 `json:"ended_at"`
} {
	s.t.Helper()
	var body struct {
		Session *struct {
			Earned  int   `json:"earned"`
			EndedAt int64 `json:"ended_at"`
		} `json:"session"`
	}
	w := s.do("licensed", http.MethodGet, "/api/farm", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		s.t.Fatal(err)
	}
	return body.Session
}

func TestSummaryKeepsTheLastRunUntilTheNextStarts(t *testing.T) {
	s := newFarmServer(t)
	if got := s.summarySession(); got != nil {
		t.Fatalf("session before any run: %+v", got)
	}
	bus := s.start()
	bus.Send(events.Event{Kind: events.CmdSuccess, Reward: 40})
	if got := s.summarySession(); got == nil || got.Earned != 40 || got.EndedAt != 0 {
		t.Fatalf("running session: %+v", got)
	}
	s.manager.Stop(s.account)
	if got := s.summarySession(); got == nil || got.Earned != 40 || got.EndedAt == 0 {
		t.Fatalf("stopped session: %+v", got)
	}
	s.start()
	if got := s.summarySession(); got == nil || got.Earned != 0 {
		t.Fatalf("new run kept old totals: %+v", got)
	}
}

func TestPauseResumeAndSkipBreak(t *testing.T) {
	s := newFarmServer(t)
	for _, path := range []string{"/api/farm/pause", "/api/farm/resume", "/api/farm/skip-break"} {
		if w := s.do("licensed", http.MethodPost, path, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s on a stopped farm: %d", path, w.Code)
		}
	}
	bus := s.start()
	if w := s.do("licensed", http.MethodPost, "/api/farm/pause", ""); w.Code != http.StatusOK || !bus.Session().Paused {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	if w := s.do("licensed", http.MethodPost, "/api/farm/resume", ""); w.Code != http.StatusOK || bus.Session().Paused {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	if w := s.do("licensed", http.MethodPost, "/api/farm/skip-break", ""); w.Code != http.StatusConflict {
		t.Fatalf("skip with no break: %d", w.Code)
	}
	bus.Send(events.Event{Kind: events.BreakStart, Reward: 300})
	if w := s.do("licensed", http.MethodPost, "/api/farm/skip-break", ""); w.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", w.Code, w.Body)
	}
	if w := s.do("licensed", http.MethodGet, "/api/farm/pause", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET pause: %d", w.Code)
	}
}
