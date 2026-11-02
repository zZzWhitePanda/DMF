package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
)

// FarmHandlers exposes a simplified single-grinder API. Each user has
// exactly ONE farm (one Discord account they grind on); this handler
// hides the underlying multi-account DB from the frontend so the UI
// doesn't need an account picker. If we ever want multi-grinder we
// just add a /api/accounts/* UI on top of the same DB.
//
// Routes:
//
//	GET    /api/farm             → farm summary ({name, is_running, configured, flow_picker, session})
//	GET    /api/farm/config      → yaml text
//	PUT    /api/farm/config      → save yaml ({config_yaml})
//	POST   /api/farm/start       → start grinder
//	POST   /api/farm/stop        → stop grinder
//	POST   /api/farm/flow-choice → answer the flow picker ({index}, -1 cancels)
type FarmHandlers struct {
	db      *db.DB
	manager *runtime.Manager
}

func NewFarmHandlers(d *db.DB, m *runtime.Manager) *FarmHandlers {
	return &FarmHandlers{db: d, manager: m}
}

func (h *FarmHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/farm", h.summary)
	mux.HandleFunc("/api/farm/config", h.config)
	mux.HandleFunc("/api/farm/start", h.start)
	mux.HandleFunc("/api/farm/stop", h.stop)
	mux.HandleFunc("/api/farm/pause", h.pause)
	mux.HandleFunc("/api/farm/resume", h.resume)
	mux.HandleFunc("/api/farm/skip-break", h.skipBreak)
	mux.HandleFunc("/api/farm/flow-choice", h.flowChoice)
}

// getOrCreate returns the user's single farm account, creating a
// default-config one on first call. All /api/farm/* handlers go
// through this so the frontend never sees an "add account" step.
func (h *FarmHandlers) getOrCreate(r *http.Request, user *db.User) (*db.DiscordAccount, error) {
	accounts, err := h.db.ListAccounts(r.Context(), user.GoogleID)
	if err != nil {
		return nil, err
	}
	if len(accounts) > 0 {
		return &accounts[0], nil
	}
	id, err := h.db.CreateAccount(r.Context(), db.DiscordAccount{
		GoogleID:   user.GoogleID,
		Name:       "my farm",
		ConfigYAML: defaultConfigYAML,
	})
	if err != nil {
		return nil, err
	}
	return h.db.GetAccount(r.Context(), id, user.GoogleID)
}

func (h *FarmHandlers) summary(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	// has_config is "do the Discord creds have real values in them?" so
	// the frontend can prompt the user to visit Settings before letting
	// them click Start.
	cfg, cfgErr := config.Parse([]byte(acc.ConfigYAML))
	configured := cfgErr == nil && len(cfg.Clusters) > 0 &&
		cfg.Clusters[0].GuildID != "" &&
		cfg.Clusters[0].ChannelID != "" &&
		cfg.Clusters[0].Master.Token != ""
	mode, currency := "classic", events.CurrencyCoins
	if cfgErr == nil {
		mode, currency = runtime.RunKind(cfg)
	}
	// run_id changes on every start, so a tab can tell a restart happened
	// even if it never saw the farm stopped.
	runID := h.manager.RunID(acc.ID)
	// flow_picker and token_rejected reach tabs that weren't connected when
	// the one-shot event went out, so a closed tab can't strand the grinder.
	bus := h.manager.Bus(acc.ID)
	// The feed can be thousands of rows and stopped tabs poll this, so it's
	// only sent to a page that asks for it.
	state := h.manager.Session(acc.ID)
	var session interface{} = state
	if r.URL.Query().Has("full") {
		session = sessionPayload(state)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":           acc.Name,
		"is_running":     runID != 0,
		"run_id":         runID,
		"configured":     configured,
		"flow_picker":    bus.FlowPicker(),
		"token_rejected": bus.TokenRejected(),
		"mode":           mode,
		"currency":       currency,
		// Also set once stopped, so the last run stays on screen.
		"session": session,
	})
}

func (h *FarmHandlers) config(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(acc.ConfigYAML))
	case http.MethodPut:
		var body struct {
			ConfigYAML string `json:"config_yaml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		// Validate the submitted yaml via the grinder's own parser so
		// bad shapes don't poison the DB.
		if _, err := config.Parse([]byte(body.ConfigYAML)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
			return
		}
		if err := h.db.UpdateAccountConfig(r.Context(), acc.ID, user.GoogleID, body.ConfigYAML); err != nil {
			writeError(w, http.StatusInternalServerError, "save failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *FarmHandlers) start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	if h.manager.IsRunning(acc.ID) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	cfg, err := config.Parse([]byte(acc.ConfigYAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config in settings: "+err.Error())
		return
	}
	if err := h.manager.Start(acc.ID, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "start failed: "+err.Error())
		return
	}
	_ = h.db.SetAccountRunning(r.Context(), acc.ID, true)
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (h *FarmHandlers) stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	h.manager.Stop(acc.ID)
	_ = h.db.SetAccountRunning(r.Context(), acc.ID, false)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *FarmHandlers) pause(w http.ResponseWriter, r *http.Request) {
	if bus := h.runningBus(w, r); bus != nil {
		bus.RequestPause(true)
		writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
	}
}

func (h *FarmHandlers) resume(w http.ResponseWriter, r *http.Request) {
	if bus := h.runningBus(w, r); bus != nil {
		bus.RequestPause(false)
		writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
	}
}

// skipBreak ends the current humanized break. 409 means no break is running.
func (h *FarmHandlers) skipBreak(w http.ResponseWriter, r *http.Request) {
	bus := h.runningBus(w, r)
	if bus == nil {
		return
	}
	if !bus.SkipBreak() {
		writeError(w, http.StatusConflict, "no break is running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
}

// runningBus returns the bus of the caller's running farm. If there isn't one
// it writes the error and returns nil.
func (h *FarmHandlers) runningBus(w http.ResponseWriter, r *http.Request) *events.Bus {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil
	}
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return nil
	}
	bus := h.manager.Bus(acc.ID)
	if bus == nil {
		writeError(w, http.StatusBadRequest, "farm not running")
	}
	return bus
}

// flowChoice answers the flow picker the grinder is blocked on. 409 means
// nothing is waiting: a second click, or a tab showing an old picker.
func (h *FarmHandlers) flowChoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := auth.UserFromContext(r.Context())
	acc, err := h.getOrCreate(r, user)
	if err != nil || acc == nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	// A pointer so a missing index can't pass as 0, the first flow.
	var body struct {
		Index *int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Index == nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	bus := h.manager.Bus(acc.ID)
	if bus == nil {
		writeError(w, http.StatusBadRequest, "farm not running")
		return
	}
	switch err := bus.AnswerFlowChoice(*body.Index); {
	case errors.Is(err, events.ErrNoFlowPicker):
		writeError(w, http.StatusConflict, "no flow choice is waiting")
	case err != nil:
		writeError(w, http.StatusBadRequest, "index out of range")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// /api/farm/ws lives in ws.go next to the other WebSocket endpoint —
// both share one pump helper.
