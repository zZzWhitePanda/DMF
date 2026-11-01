// Package runtime is the per-user grinder instance manager. The HTTP
// API uses it to start / stop / inspect running grinders, and the
// WebSocket handler uses it to subscribe to a grinder's event stream.
package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/cluster"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

// Manager tracks running grinder instances by their database account id.
// Thread-safe — HTTP handlers can call Start/Stop concurrently.
type Manager struct {
	mu      sync.Mutex
	entries map[int64]*entry
	lastRun int64
	// finished keeps each account's last bus, so its session outlives Stop.
	finished map[int64]*events.Bus
}

type entry struct {
	bus    *events.Bus
	cancel context.CancelFunc
	runID  int64
}

// New returns an empty Manager.
func New() *Manager {
	return &Manager{entries: make(map[int64]*entry), finished: make(map[int64]*events.Bus)}
}

// RunKind works out a run's mode and its one currency, with the same
// precedence as commands.Build: flow, then fish, then the classic commands.
func RunKind(cfg *config.Config) (mode, currency string) {
	commands := cfg.Features.Commands
	switch {
	case commands["flow"]:
		return "flow", events.CurrencyCoins
	case commands["fish"]:
		if strings.EqualFold(cfg.Features.Fishing.SellMode, "coins") {
			return "fish", events.CurrencyCoins
		}
		return "fish", events.CurrencyTokens
	}
	return "classic", events.CurrencyCoins
}

// Start launches a grinder for the given account id with the given
// parsed config. Returns an error if the grinder is already running or
// the cluster fails to initialize. The bus is retained for WebSocket
// subscribers until Stop is called, which ends their subscriptions.
func (m *Manager) Start(accountID int64, cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[accountID]; ok {
		return fmt.Errorf("grinder for account %d already running", accountID)
	}
	bus := events.NewBus(512)
	bus.SetRun(RunKind(cfg))
	ctx, cancel := context.WithCancel(context.Background())
	// We run every configured cluster. For MVP each DiscordAccount
	// stores a single-cluster yaml so this is one cluster per account.
	for i := range cfg.Clusters {
		r, err := cluster.New(&cfg.Clusters[i], cfg, bus)
		if err != nil {
			cancel()
			return fmt.Errorf("cluster init: %w", err)
		}
		go r.Run(ctx)
	}
	// Based on the clock so ids stay unique across server restarts: a tab
	// that outlives the server must still see the next run as a new one.
	m.lastRun = max(m.lastRun+1, time.Now().UnixMilli())
	m.entries[accountID] = &entry{bus: bus, cancel: cancel, runID: m.lastRun}
	delete(m.finished, accountID)
	log.WithField("account_id", accountID).Info("grinder started")
	return nil
}

// Stop cancels a running grinder for the given account. No-op if the
// grinder isn't running.
func (m *Manager) Stop(accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[accountID]
	if !ok {
		return
	}
	e.cancel()
	e.bus.Close()
	m.finished[accountID] = e.bus
	delete(m.entries, accountID)
	log.WithField("account_id", accountID).Info("grinder stopped")
}

// IsRunning reports whether a grinder is active for an account.
func (m *Manager) IsRunning(accountID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[accountID]
	return ok
}

// RunID identifies the current run of an account's grinder, so a client can
// tell a restart from the same run carrying on. Returns 0 if not running.
func (m *Manager) RunID(accountID int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[accountID]
	if !ok {
		return 0
	}
	return e.runID
}

// Bus returns the event bus for a running grinder so WebSocket clients
// can subscribe to its stream. Returns nil if not running.
func (m *Manager) Bus(accountID int64) *events.Bus {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[accountID]
	if !ok {
		return nil
	}
	return e.bus
}

// Session returns the running or most recent run's state, or nil if the
// account hasn't run since the server started.
func (m *Manager) Session(accountID int64) *events.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	bus := m.finished[accountID]
	if e, ok := m.entries[accountID]; ok {
		bus = e.bus
	}
	if bus == nil {
		return nil
	}
	s := bus.Session()
	return &s
}

// StopAll cancels every running grinder. Called from the server's
// graceful shutdown path.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.entries {
		e.cancel()
		e.bus.Close()
		delete(m.entries, id)
	}
}
