package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

// serve puts handler on a real listener, since sockets can't use a recorder.
func serve(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{"Cookie": {testCookie + "=licensed"}}
	conn, res, err := websocket.DefaultDialer.Dial(url, header)
	if conn != nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, res, err
}

// connect opens a socket on the farm route, as a dashboard tab does.
func (s *farmServer) connect(url string) *websocket.Conn {
	s.t.Helper()
	conn, _, err := dial(s.t, url+"/api/farm/ws")
	if err != nil {
		s.t.Fatal(err)
	}
	return conn
}

func reward(n int) events.Event {
	return events.Event{Kind: events.CmdSuccess, Reward: n}
}

// nextReward reads one event frame off a socket.
func nextReward(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	var ev struct {
		Reward int `json:"reward"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("reading event: %v", err)
	}
	return ev.Reward
}

// expectFarmStopped asserts the server ended the socket with a normal closure.
func expectFarmStopped(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("got a frame instead of the close: %s", msg)
	}
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("socket did not end with a normal closure: %v", err)
	}
}

func (s *farmServer) runID() int64 {
	s.t.Helper()
	var body struct {
		IsRunning bool  `json:"is_running"`
		RunID     int64 `json:"run_id"`
	}
	w := s.do("licensed", http.MethodGet, "/api/farm", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		s.t.Fatal(err)
	}
	if body.IsRunning != (body.RunID != 0) {
		s.t.Fatalf("is_running %v disagrees with run_id %d", body.IsRunning, body.RunID)
	}
	return body.RunID
}

func TestEverySocketGetsEveryEvent(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	bus := s.start()
	first, second := s.connect(url), s.connect(url)

	for n := 1; n <= 50; n++ {
		bus.Send(reward(n))
	}
	for n := 1; n <= 50; n++ {
		if got := nextReward(t, first); got != n {
			t.Fatalf("first socket got %d, want %d", got, n)
		}
		if got := nextReward(t, second); got != n {
			t.Fatalf("second socket got %d, want %d", got, n)
		}
	}
}

func TestFirstSocketGetsEventsFromBeforeItConnected(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	bus := s.start()
	bus.Send(reward(1))
	bus.Send(reward(2))

	first := s.connect(url)
	if got := nextReward(t, first); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	if got := nextReward(t, first); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}

	second := s.connect(url)
	bus.Send(reward(3))
	if got := nextReward(t, second); got != 3 {
		t.Fatalf("second socket got %d, want only the live 3", got)
	}
	if got := nextReward(t, first); got != 3 {
		t.Fatalf("first socket got %d, want 3", got)
	}
}

func TestSocketEndsWhenTheFarmStops(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	bus := s.start()
	first, second := s.connect(url), s.connect(url)

	bus.Send(reward(100))
	s.manager.Stop(s.account)

	for _, conn := range []*websocket.Conn{first, second} {
		if got := nextReward(t, conn); got != 100 {
			t.Fatalf("got %d, want the last event before the close", got)
		}
		expectFarmStopped(t, conn)
	}
}

func TestSocketEndsOnStopAll(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	s.start()
	conn := s.connect(url)

	s.manager.StopAll()
	expectFarmStopped(t, conn)
}

func TestSocketOnAStoppedFarmIsRefused(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	s.start()
	s.manager.Stop(s.account)

	_, res, err := dial(t, url+"/api/farm/ws")
	if err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %v, %+v, want a 400", err, res)
	}
}

// A handler can look the bus up just before the farm stops, then upgrade.
func TestSocketThatLosesTheRaceWithStopEndsAtOnce(t *testing.T) {
	s := newFarmServer(t)
	bus := s.start()
	s.manager.Stop(s.account)

	h := NewWSHandlers(s.db, s.manager, nil)
	url := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.pump(w, r, bus)
	}))
	conn, _, err := dial(t, url)
	if err != nil {
		t.Fatal(err)
	}
	expectFarmStopped(t, conn)
}

func TestRestartEndsOldSocketsAndStartsANewRun(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	if got := s.runID(); got != 0 {
		t.Fatalf("stopped farm has run id %d", got)
	}

	s.start()
	run := s.runID()
	if run == 0 {
		t.Fatal("running farm has no run id")
	}
	old := s.connect(url)

	s.manager.Stop(s.account)
	bus := s.start()
	bus.Send(reward(200))

	expectFarmStopped(t, old)
	if got := s.runID(); got == run || got == 0 {
		t.Fatalf("run id after restart = %d, was %d", got, run)
	}
	if got := nextReward(t, s.connect(url)); got != 200 {
		t.Fatalf("new socket got %d, want 200", got)
	}
}
