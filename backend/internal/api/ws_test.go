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

// openingFrame is the part of a socket's first message tests look at.
type openingFrame struct {
	Kind    string `json:"kind"`
	Session struct {
		Earned int `json:"earned"`
		Recent []struct {
			ID     int64 `json:"id"`
			Reward int   `json:"reward"`
		} `json:"recent"`
	} `json:"session"`
}

func readOpening(t *testing.T, conn *websocket.Conn) openingFrame {
	t.Helper()
	var first openingFrame
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatalf("reading opening frame: %v", err)
	}
	if first.Kind != "session" {
		t.Fatalf("first frame was %q, want session", first.Kind)
	}
	return first
}

// connect opens a socket on the farm route, as a dashboard tab does, and reads
// past the opening session.
func (s *farmServer) connect(url string) *websocket.Conn {
	conn, _ := s.connectWithSession(url)
	return conn
}

func (s *farmServer) connectWithSession(url string) (*websocket.Conn, openingFrame) {
	s.t.Helper()
	conn, _, err := dial(s.t, url+"/api/farm/ws")
	if err != nil {
		s.t.Fatal(err)
	}
	return conn, readOpening(s.t, conn)
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

// Closing every tab must lose nothing: a socket opened later starts with the
// whole run, and live events carry on from there without repeats.
func TestSocketOpensWithTheRunSoFar(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	bus := s.start()
	bus.Send(reward(1))
	bus.Send(reward(2))

	first, opening := s.connectWithSession(url)
	if opening.Session.Earned != 3 || len(opening.Session.Recent) != 2 {
		t.Fatalf("opened with %+v", opening.Session)
	}
	if ids := opening.Session.Recent; ids[0].ID >= ids[1].ID {
		t.Fatalf("rows out of order: %+v", ids)
	}

	second, opening := s.connectWithSession(url)
	if opening.Session.Earned != 3 {
		t.Fatalf("second tab opened with %+v", opening.Session)
	}
	bus.Send(reward(3))
	for _, conn := range []*websocket.Conn{first, second} {
		if got := nextReward(t, conn); got != 3 {
			t.Fatalf("got %d, want only the live 3", got)
		}
	}
}

func TestEventFramesCarryTheSession(t *testing.T) {
	s := newFarmServer(t)
	url := serve(t, s.handler)
	bus := s.start()
	conn := s.connect(url)

	bus.Send(reward(5))
	var frame struct {
		ID      int64 `json:"id"`
		Session struct {
			Earned int `json:"earned"`
		} `json:"session"`
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.ID == 0 || frame.Session.Earned != 5 {
		t.Fatalf("got %+v", frame)
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
	readOpening(t, conn)
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
	if _, opening := s.connectWithSession(url); opening.Session.Earned != 200 {
		t.Fatalf("new socket opened with %+v, want earned 200", opening.Session)
	}
}
