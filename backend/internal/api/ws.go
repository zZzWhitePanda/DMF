package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
	log "github.com/sirupsen/logrus"
)

// WSHandlers streams live grinder events to the browser dashboard.
type WSHandlers struct {
	db       *db.DB
	manager  *runtime.Manager
	upgrader websocket.Upgrader
}

func NewWSHandlers(d *db.DB, m *runtime.Manager, allowedOrigins []string) *WSHandlers {
	allow := map[string]bool{}
	for _, o := range allowedOrigins {
		allow[strings.ToLower(o)] = true
	}
	return &WSHandlers{
		db:      d,
		manager: m,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  2048,
			WriteBufferSize: 2048,
			CheckOrigin: func(r *http.Request) bool {
				if len(allow) == 0 {
					return true // dev
				}
				return allow[strings.ToLower(r.Header.Get("Origin"))]
			},
		},
	}
}

// Register attaches both WS endpoints:
//
//	/api/ws/{account_id}  — legacy, multi-account aware (keeps working)
//	/api/farm/ws          — single-farm shorthand (the simplified API)
func (h *WSHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/ws/", h.wsByID)
	mux.HandleFunc("/api/farm/ws", h.wsFarm)
}

// wsByID looks up the account by URL path and streams its bus.
func (h *WSHandlers) wsByID(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/ws/")
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	acc, err := h.db.GetAccount(r.Context(), id, user.GoogleID)
	if err != nil || acc == nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	bus := h.manager.Bus(acc.ID)
	if bus == nil {
		writeError(w, http.StatusBadRequest, "grinder not running for this account")
		return
	}
	h.pump(w, r, bus)
}

// wsFarm looks up the user's single farm account (creating one if
// missing) and streams its bus.
func (h *WSHandlers) wsFarm(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	accounts, err := h.db.ListAccounts(r.Context(), user.GoogleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "farm lookup failed")
		return
	}
	if len(accounts) == 0 {
		writeError(w, http.StatusBadRequest, "farm not configured")
		return
	}
	acc := accounts[0]
	bus := h.manager.Bus(acc.ID)
	if bus == nil {
		writeError(w, http.StatusBadRequest, "farm not running")
		return
	}
	h.pump(w, r, bus)
}

// pump upgrades to a websocket + streams events until the client
// disconnects or the grinder stops (bus channel closes).
func (h *WSHandlers) pump(w http.ResponseWriter, r *http.Request, bus *events.Bus) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Debug("ws upgrade failed")
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		conn.SetReadLimit(1024)
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		for {
			if _, _, err := conn.NextReader(); err != nil {
				close(done)
				return
			}
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case ev, ok := <-bus.Chan():
			if !ok {
				return
			}
			payload, err := encodeEvent(ev)
			if err != nil {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
	}
}

func encodeEvent(ev events.Event) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"kind":     kindToString(ev.Kind),
		"command":  ev.Command,
		"message":  ev.Message,
		"reward":   ev.Reward,
		"time":     ev.Time.Unix(),
		"activity": ev.Activity,
		"items":    ev.Items,
		"choices":  ev.Choices,
		"outcome":  ev.Outcome,
	})
}

func kindToString(k events.Kind) string {
	switch k {
	case events.Info:
		return "info"
	case events.Success:
		return "success"
	case events.Warn:
		return "warn"
	case events.Err:
		return "error"
	case events.CmdStart:
		return "cmd_start"
	case events.CmdSuccess:
		return "cmd_success"
	case events.CmdFail:
		return "cmd_fail"
	case events.CmdCooldown:
		return "cmd_cooldown"
	case events.Captcha:
		return "captcha"
	case events.ShiftOn:
		return "shift_on"
	case events.ShiftOff:
		return "shift_off"
	case events.GatewayUp:
		return "gateway_up"
	case events.GatewayDown:
		return "gateway_down"
	case events.Activity:
		return "activity"
	case events.Item:
		return "item"
	case events.HighlowDecision:
		return "highlow_decision"
	case events.HighlowOutcome:
		return "highlow_outcome"
	case events.FlowPickerNeeded:
		return "flow_picker"
	case events.FishToken:
		return "fish_token"
	case events.FishCaught:
		return "fish_caught"
	case events.RodBroken:
		return "rod_broken"
	case events.BreakStart:
		return "break_start"
	case events.BreakEnd:
		return "break_end"
	}
	return "unknown"
}
