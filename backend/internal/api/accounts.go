package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
)

// AccountHandlers wires the /api/accounts routes. Every route here is
// mounted behind RequireAuth + RequireLicensed.
type AccountHandlers struct {
	db      *db.DB
	manager *runtime.Manager
}

func NewAccountHandlers(d *db.DB, m *runtime.Manager) *AccountHandlers {
	return &AccountHandlers{db: d, manager: m}
}

// Register attaches account routes to a mux. mux here should already be
// a sub-mux wrapped in RequireAuth + RequireLicensed by the caller.
func (h *AccountHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/accounts", h.collection) // GET list, POST create
	mux.HandleFunc("/api/accounts/", h.resource)  // /api/accounts/{id}, /start, /stop, config
}

// collection dispatches on method: GET list, POST create.
func (h *AccountHandlers) collection(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	switch r.Method {
	case http.MethodGet:
		accounts, err := h.db.ListAccounts(r.Context(), user.GoogleID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list failed")
			return
		}
		out := make([]map[string]interface{}, 0, len(accounts))
		for _, a := range accounts {
			out = append(out, h.accountToMap(a))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var body struct {
			Name       string `json:"name"`
			ConfigYAML string `json:"config_yaml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.Name == "" {
			writeError(w, http.StatusBadRequest, "name required")
			return
		}
		if body.ConfigYAML == "" {
			body.ConfigYAML = defaultConfigYAML
		}
		id, err := h.db.CreateAccount(r.Context(), db.DiscordAccount{
			GoogleID:   user.GoogleID,
			Name:       body.Name,
			ConfigYAML: body.ConfigYAML,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create failed")
			return
		}
		acc, _ := h.db.GetAccount(r.Context(), id, user.GoogleID)
		writeJSON(w, http.StatusCreated, h.accountToMap(*acc))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// resource routes /api/accounts/{id}, /api/accounts/{id}/start,
// /api/accounts/{id}/stop, /api/accounts/{id}/config.
func (h *AccountHandlers) resource(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	acc, err := h.db.GetAccount(r.Context(), id, user.GoogleID)
	if err != nil || acc == nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	// Sub-route switch.
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	switch sub {
	case "":
		h.resourceRoot(w, r, user, acc)
	case "start":
		h.start(w, r, acc)
	case "stop":
		h.stop(w, r, acc)
	case "config":
		h.config(w, r, user, acc)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *AccountHandlers) resourceRoot(w http.ResponseWriter, r *http.Request, user *db.User, acc *db.DiscordAccount) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.accountToMap(*acc))
	case http.MethodDelete:
		if h.manager.IsRunning(acc.ID) {
			h.manager.Stop(acc.ID)
		}
		if err := h.db.DeleteAccount(r.Context(), acc.ID, user.GoogleID); err != nil {
			writeError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *AccountHandlers) start(w http.ResponseWriter, r *http.Request, acc *db.DiscordAccount) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.manager.IsRunning(acc.ID) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	cfg, err := config.Parse([]byte(acc.ConfigYAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
		return
	}
	if err := h.manager.Start(acc.ID, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "start failed: "+err.Error())
		return
	}
	_ = h.db.SetAccountRunning(r.Context(), acc.ID, true)
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (h *AccountHandlers) stop(w http.ResponseWriter, r *http.Request, acc *db.DiscordAccount) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.manager.Stop(acc.ID)
	_ = h.db.SetAccountRunning(r.Context(), acc.ID, false)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (h *AccountHandlers) config(w http.ResponseWriter, r *http.Request, user *db.User, acc *db.DiscordAccount) {
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
		// Validate before saving.
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

// accountToMap renders a DB row as the frontend's expected JSON shape.
// Config yaml is omitted from the summary — the dedicated /config
// endpoint streams it when the user opens the settings editor.
func (h *AccountHandlers) accountToMap(a db.DiscordAccount) map[string]interface{} {
	return map[string]interface{}{
		"id":         a.ID,
		"name":       a.Name,
		"is_running": h.manager.IsRunning(a.ID),
		"created_at": a.CreatedAt.Unix(),
		"updated_at": a.UpdatedAt.Unix(),
	}
}

// defaultConfigYAML is the seed config a new account gets. Minimal
// shape — the user must fill in token / channel / guild via the UI.
const defaultConfigYAML = `clusters:
  - name: main
    guild_id: ""
    channel_id: ""
    master:
      name: alpha
      token: ""
    shift:
      enabled: true
      on_duration: 45m
      off_duration: 15m
      variation: 5m
humanize:
  typing_indicator: true
  min_delay: 2500ms
  max_delay: 5500ms
  typing_variance: 1500ms
captcha:
  webhook: ""
  pause_on_detect: true
  mention_user_id: ""
features:
  commands:
    beg: true
    search: true
    hunt: true
    fish: false
    flow: false
  breaks:
    enabled: true
  random_events: true
  random_event_handlers:
    first_to_say: true
    reverse_reverse: true
    fortnite_dance: true
    punch_pepe: true
    steal_mels_beard: true
    scrambled_eggs: true
    trivia_night: true
log:
  level: info
`
