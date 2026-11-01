// Package runtime is the per-user grinder instance manager. The HTTP
// API uses it to start / stop / inspect running grinders, and the
// WebSocket handler uses it to subscribe to a grinder's event stream.
package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/cluster"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	log "github.com/sirupsen/logrus"
)

// Manager tracks running grinder instances by their database account id.
// Thread-safe — HTTP handlers can call Start/Stop concurrently.
type Manager struct {
	mu       sync.Mutex
	entries  map[int64]*entry
}

type entry struct {
	bus    *events.Bus
	cancel context.CancelFunc
}

// New returns an empty Manager.
func New() *Manager {
	return &Manager{entries: make(map[int64]*entry)}
}

// Start launches a grinder for the given account id with the given
// parsed config. Returns an error if the grinder is already running or
// the cluster fails to initialize. The bus is retained for WebSocket
// subscribers until Stop is called.
func (m *Manager) Start(accountID int64, cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[accountID]; ok {
		return fmt.Errorf("grinder for account %d already running", accountID)
	}
	bus := events.NewBus(512)
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
	m.entries[accountID] = &entry{bus: bus, cancel: cancel}
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

// StopAll cancels every running grinder. Called from the server's
// graceful shutdown path.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.entries {
		e.cancel()
		delete(m.entries, id)
	}
}
