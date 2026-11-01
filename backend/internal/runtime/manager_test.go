package runtime

import (
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

// start runs a farm with no clusters, so no grinder touches the bus.
func start(t *testing.T, m *Manager, id int64) *events.Bus {
	t.Helper()
	if err := m.Start(id, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	return m.Bus(id)
}

func expectEnded(t *testing.T, feed <-chan events.Event) {
	t.Helper()
	select {
	case _, ok := <-feed:
		if ok {
			t.Fatal("got an event, want the subscription closed")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription outlived the farm")
	}
}

func TestStopEndsSubscriptions(t *testing.T) {
	m := New()
	bus := start(t, m, 1)
	first, cancelFirst := bus.Subscribe()
	defer cancelFirst()
	second, cancelSecond := bus.Subscribe()
	defer cancelSecond()

	m.Stop(1)
	expectEnded(t, first)
	expectEnded(t, second)
	if m.Bus(1) != nil {
		t.Fatal("stopped farm still has a bus")
	}
}

func TestStopAllEndsSubscriptions(t *testing.T) {
	m := New()
	feed, cancel := start(t, m, 1).Subscribe()
	defer cancel()
	other, cancelOther := start(t, m, 2).Subscribe()
	defer cancelOther()

	m.StopAll()
	expectEnded(t, feed)
	expectEnded(t, other)
}

func TestStopLeavesOtherFarmsRunning(t *testing.T) {
	m := New()
	start(t, m, 1)
	bus := start(t, m, 2)
	feed, cancel := bus.Subscribe()
	defer cancel()

	m.Stop(1)
	bus.Send(events.Event{Kind: events.Info})
	select {
	case _, ok := <-feed:
		if !ok {
			t.Fatal("stopping one farm ended another's subscription")
		}
	case <-time.After(time.Second):
		t.Fatal("no event from the farm still running")
	}
}

func TestRunIDIsNewOnEveryStart(t *testing.T) {
	m := New()
	if got := m.RunID(1); got != 0 {
		t.Fatalf("run id before start = %d", got)
	}

	seen := map[int64]bool{}
	for range 100 {
		start(t, m, 1)
		id := m.RunID(1)
		if id == 0 || seen[id] {
			t.Fatalf("run id %d repeated or zero", id)
		}
		seen[id] = true
		m.Stop(1)
		if got := m.RunID(1); got != 0 {
			t.Fatalf("run id after stop = %d", got)
		}
	}
}
