package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	log "github.com/sirupsen/logrus"
)

// AuthHandlers wires HTTP routes for the Google OAuth flow + session
// introspection.
type AuthHandlers struct {
	provider *auth.GoogleProvider
	db       *db.DB
	cfg      auth.Config
}

func NewAuthHandlers(provider *auth.GoogleProvider, d *db.DB, cfg auth.Config) *AuthHandlers {
	return &AuthHandlers{provider: provider, db: d, cfg: cfg}
}

// Register attaches auth routes to the mux. All routes are public (no
// middleware wrapping — these ARE the auth boundary).
func (h *AuthHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/auth/google/start", h.start)
	mux.HandleFunc("/auth/google/callback", h.callback)
	mux.HandleFunc("/auth/logout", h.logout)
	mux.HandleFunc("/auth/me", h.me)
}

// start initiates the OAuth dance. Generates a state token, stores it
// in a short-lived cookie, redirects to Google's consent page.
func (h *AuthHandlers) start(w http.ResponseWriter, r *http.Request) {
	// Also capture the client-side HWID that the login page posted. The
	// browser sends it as a query parameter (?hwid=...) because
	// browsers can't attach a POST body to a GET redirect.
	hwid := r.URL.Query().Get("hwid")
	state := auth.RandomState(24)
	// state cookie carries both the CSRF token and the HWID,
	// pipe-separated. Short TTL — 10 min is more than enough for the
	// user to complete Google's consent screen.
	h.provider.SetCookie(w, "oauth_state", state+"|"+hwid, 10*time.Minute)
	http.Redirect(w, r, h.provider.AuthURL(state), http.StatusTemporaryRedirect)
}

// callback is where Google redirects after consent. We verify state,
// exchange code for tokens, fetch the user's profile, upsert them in
// our users table, flip is_admin if they're the configured admin
// email, create a session, set the session cookie, redirect to /.
func (h *AuthHandlers) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code or state")
		return
	}
	// Verify state cookie.
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "state cookie missing")
		return
	}
	parts := strings.SplitN(cookie.Value, "|", 2)
	if len(parts) != 2 || parts[0] != state {
		writeError(w, http.StatusBadRequest, "state mismatch")
		return
	}
	hwid := parts[1]
	h.provider.ClearCookie(w, "oauth_state")

	// Trade code for tokens.
	tok, err := h.provider.Exchange(r.Context(), code)
	if err != nil {
		log.WithError(err).Warn("oauth exchange failed")
		writeError(w, http.StatusBadGateway, "oauth exchange failed")
		return
	}
	user, err := h.provider.FetchUserInfo(r.Context(), tok)
	if err != nil {
		log.WithError(err).Warn("fetch userinfo failed")
		writeError(w, http.StatusBadGateway, "userinfo fetch failed")
		return
	}

	// Upsert user. Flip is_admin if this is the configured admin email.
	isAdmin := strings.EqualFold(user.Email, h.cfg.AdminEmail)
	if err := h.db.UpsertUser(r.Context(), db.User{
		GoogleID:    user.Sub,
		Email:       user.Email,
		Name:        user.Name,
		PictureURL:  user.Picture,
		IsAdmin:     isAdmin,
	}); err != nil {
		log.WithError(err).Warn("upsert user failed")
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// If the user already existed we still want to re-apply admin
	// status in case the ADMIN_EMAIL env var changed. Upsert's ON
	// CONFLICT doesn't touch is_admin to protect manual grants, so
	// explicitly set it here when the config email matches.
	if isAdmin {
		_ = h.db.SetAdmin(r.Context(), user.Sub, true)
	}

	// Create session.
	sess := db.Session{
		ID:        auth.RandomState(32),
		GoogleID:  user.Sub,
		HWID:      hwid,
		ExpiresAt: time.Now().Add(h.cfg.SessionDuration),
		CreatedAt: time.Now(),
	}
	if err := h.db.CreateSession(r.Context(), sess); err != nil {
		log.WithError(err).Warn("create session failed")
		writeError(w, http.StatusInternalServerError, "session error")
		return
	}
	h.provider.SetCookie(w, h.cfg.SessionCookie, sess.ID, h.cfg.SessionDuration)
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// logout deletes the server-side session + clears the cookie.
func (h *AuthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(h.cfg.SessionCookie)
	if err == nil && cookie.Value != "" {
		_ = h.db.DeleteSession(r.Context(), cookie.Value)
	}
	h.provider.ClearCookie(w, h.cfg.SessionCookie)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// me returns the current user's profile + license state. The frontend
// calls this on load to decide which view to render (login / license
// prompt / dashboard).
func (h *AuthHandlers) me(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(h.cfg.SessionCookie)
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"signed_in": false})
		return
	}
	sess, err := h.db.GetSession(r.Context(), cookie.Value)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"signed_in": false})
		return
	}
	user, err := h.db.GetUser(r.Context(), sess.GoogleID)
	if err != nil || user == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"signed_in": false})
		return
	}
	lic, _ := h.db.LicenseForUser(r.Context(), user.GoogleID)
	resp := map[string]interface{}{
		"signed_in": true,
		"user": map[string]interface{}{
			"email":    user.Email,
			"name":     user.Name,
			"picture":  user.PictureURL,
			"is_admin": user.IsAdmin,
			"is_owner": strings.EqualFold(user.Email, h.cfg.AdminEmail),
		},
	}
	if user.IsAdmin {
		resp["license_status"] = "admin"
	} else if lic == nil {
		resp["license_status"] = "none"
	} else if !lic.IsActive() {
		resp["license_status"] = "inactive"
	} else if lic.HWID != "" && lic.HWID != sess.HWID {
		// 24h reset cooldown math — frontend uses this to show a
		// countdown on the reset button.
		cooldownUntil := lic.LastResetAt.Add(24 * time.Hour)
		resp["license_status"] = "hwid_mismatch"
		resp["reset_available_at"] = cooldownUntil.Unix()
	} else {
		resp["license_status"] = "ok"
	}
	writeJSON(w, http.StatusOK, resp)
}
