package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// GatewayURL is the default Discord gateway endpoint.
const GatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// closeAuthFailed is the gateway close code for a token Discord won't accept.
const closeAuthFailed = 4004

// ErrAuthFailed is returned by Connect when Discord rejects the token.
var ErrAuthFailed = errors.New("discord rejected the token")

// EventHandler is invoked for a dispatched gateway event's data payload.
type EventHandler func(data json.RawMessage)

// Gateway handles the WS connection for one account, including heartbeats, resume, and dispatch.
type Gateway struct {
	token string
	proxy string
	log   *log.Entry

	conn   *websocket.Conn
	sendMu sync.Mutex

	sessionID string
	resumeURL string
	lastSeq   *int

	heartbeatInterval time.Duration

	handlers map[string][]EventHandler
	hMu      sync.RWMutex

	ready chan struct{}
	once  sync.Once

	// OnDisconnect, if set, is called each time the socket drops and a
	// reconnect is about to be tried. Set before Connect.
	OnDisconnect func(error)
}

// NewGateway builds a new Gateway. logger is required.
func NewGateway(token, proxy string, logger *log.Entry) *Gateway {
	return &Gateway{
		token:    token,
		proxy:    proxy,
		log:      logger,
		handlers: make(map[string][]EventHandler),
		ready:    make(chan struct{}),
	}
}

// On registers an event handler. Call before Connect.
func (g *Gateway) On(event string, h EventHandler) {
	g.hMu.Lock()
	defer g.hMu.Unlock()
	g.handlers[event] = append(g.handlers[event], h)
}

// Ready returns a channel closed once READY has been received.
func (g *Gateway) Ready() <-chan struct{} { return g.ready }

// SessionID returns the current session id (valid once Ready() has fired).
func (g *Gateway) SessionID() string { return g.sessionID }

// isAuthFailure reports whether err is Discord closing the socket over a bad token.
func isAuthFailure(err error) bool {
	var closed *websocket.CloseError
	return errors.As(err, &closed) && closed.Code == closeAuthFailed
}

// Connect runs until ctx is done or a fatal error. Reconnects with backoff on
// transient errors. A rejected token is fatal (ErrAuthFailed): retrying the
// same token can't succeed.
func (g *Gateway) Connect(ctx context.Context) error {
	backoff := 5 * time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := g.run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isAuthFailure(err) {
			return ErrAuthFailed
		}
		g.log.WithError(err).Warnf("gateway run ended, reconnecting in %s", backoff)
		if g.OnDisconnect != nil {
			g.OnDisconnect(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

func (g *Gateway) run(ctx context.Context) error {
	dialer := *websocket.DefaultDialer
	if g.proxy != "" {
		pu, err := url.Parse(g.proxy)
		if err != nil {
			return fmt.Errorf("proxy url: %w", err)
		}
		dialer.Proxy = http.ProxyURL(pu)
	}
	target := GatewayURL
	if g.resumeURL != "" && g.sessionID != "" {
		target = g.resumeURL + "/?v=10&encoding=json"
	}

	conn, _, err := dialer.Dial(target, http.Header{"User-Agent": {"Mozilla/5.0"}})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	g.conn = conn
	defer conn.Close()

	var hello GatewayEvent
	if err := conn.ReadJSON(&hello); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != OpHello {
		return fmt.Errorf("expected hello, got op %d", hello.Op)
	}
	var h Hello
	if err := json.Unmarshal(hello.D, &h); err != nil {
		return fmt.Errorf("parse hello: %w", err)
	}
	g.heartbeatInterval = time.Duration(h.HeartbeatInterval) * time.Millisecond
	g.log.WithField("interval", g.heartbeatInterval).Debug("hello received")

	if g.sessionID != "" && g.lastSeq != nil {
		if err := g.sendResume(); err != nil {
			return fmt.Errorf("resume: %w", err)
		}
	} else {
		if err := g.sendIdentify(); err != nil {
			return fmt.Errorf("identify: %w", err)
		}
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go g.heartbeatLoop(hbCtx)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var ev GatewayEvent
		if err := conn.ReadJSON(&ev); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if ev.S != nil {
			g.lastSeq = ev.S
		}
		if err := g.handleEvent(&ev); err != nil {
			return err
		}
	}
}

func (g *Gateway) handleEvent(ev *GatewayEvent) error {
	switch ev.Op {
	case OpHeartbeat:
		return g.sendHeartbeat()
	case OpReconnect:
		return fmt.Errorf("server requested reconnect")
	case OpInvalidSession:
		g.sessionID = ""
		g.lastSeq = nil
		return fmt.Errorf("invalid session")
	case OpHeartbeatAck:
		return nil
	case OpDispatch:
		if ev.T == "READY" {
			var r Ready
			if err := json.Unmarshal(ev.D, &r); err == nil {
				g.sessionID = r.SessionID
				g.resumeURL = r.ResumeURL
				g.log.WithField("user", r.User.Username).Info("gateway ready")
				g.once.Do(func() { close(g.ready) })
			}
		}
		g.dispatch(ev.T, ev.D)
	}
	return nil
}

func (g *Gateway) dispatch(event string, data json.RawMessage) {
	g.hMu.RLock()
	handlers := append([]EventHandler{}, g.handlers[event]...)
	g.hMu.RUnlock()
	for _, h := range handlers {
		go func(h EventHandler) {
			defer func() {
				if r := recover(); r != nil {
					g.log.WithField("event", event).Errorf("handler panic: %v", r)
				}
			}()
			h(data)
		}(h)
	}
}

func (g *Gateway) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(g.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := g.sendHeartbeat(); err != nil {
				g.log.WithError(err).Warn("heartbeat failed")
				return
			}
		}
	}
}

func (g *Gateway) sendHeartbeat() error {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	var seq interface{}
	if g.lastSeq != nil {
		seq = *g.lastSeq
	}
	return g.conn.WriteJSON(map[string]interface{}{
		"op": OpHeartbeat,
		"d":  seq,
	})
}

func (g *Gateway) sendIdentify() error {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	identify := map[string]interface{}{
		"op": OpIdentify,
		"d": map[string]interface{}{
			"token":        g.token,
			"capabilities": 16381,
			"properties": map[string]interface{}{
				"os":                       "Windows",
				"browser":                  "Chrome",
				"device":                   "",
				"system_locale":            "en-US",
				"browser_user_agent":       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"browser_version":          "120.0.0.0",
				"os_version":               "10",
				"referrer":                 "",
				"referring_domain":         "",
				"referrer_current":         "",
				"referring_domain_current": "",
				"release_channel":          "stable",
				"client_build_number":      260292,
				"client_event_source":      nil,
			},
			"presence": map[string]interface{}{
				"status":     "online",
				"since":      0,
				"activities": []interface{}{},
				"afk":        false,
			},
			"compress": false,
			"client_state": map[string]interface{}{
				"guild_versions":              map[string]interface{}{},
				"highest_last_message_id":     "0",
				"read_state_version":          0,
				"user_guild_settings_version": -1,
				"user_settings_version":       -1,
				"private_channels_version":    "0",
				"api_code_version":            0,
			},
		},
	}
	return g.conn.WriteJSON(identify)
}

func (g *Gateway) sendResume() error {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	seq := 0
	if g.lastSeq != nil {
		seq = *g.lastSeq
	}
	return g.conn.WriteJSON(map[string]interface{}{
		"op": OpResume,
		"d": map[string]interface{}{
			"token":      g.token,
			"session_id": g.sessionID,
			"seq":        seq,
		},
	})
}
