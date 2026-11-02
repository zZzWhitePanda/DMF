package auth

import (
	"context"
	"net/http"

	"github.com/zZzWhitePanda/DMF/backend/internal/db"
)

// ctxKey is a private type for context keys to avoid collisions.
type ctxKey int

const (
	// ctxUserKey holds the authenticated *db.User in the request context.
	ctxUserKey ctxKey = iota
	// ctxSessionKey holds the live *db.Session in the request context.
	ctxSessionKey
)

// UserFromContext returns the authenticated user from a request context
// populated by RequireAuth. Returns nil if no user is attached.
func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(ctxUserKey).(*db.User)
	return u
}

// SessionFromContext returns the live session attached by RequireAuth.
func SessionFromContext(ctx context.Context) *db.Session {
	s, _ := ctx.Value(ctxSessionKey).(*db.Session)
	return s
}

// Middleware chains auth checks for HTTP handlers.
type Middleware struct {
	provider *GoogleProvider
	db       *db.DB
	cookie   string
}

// NewMiddleware constructs an auth middleware bound to a provider + db.
func NewMiddleware(p *GoogleProvider, d *db.DB) *Middleware {
	return &Middleware{provider: p, db: d, cookie: p.cfg.SessionCookie}
}

// RequireAuth is HTTP middleware that validates the session cookie and
// loads the User + Session into the request context. Unauthenticated
// requests get a 401 JSON response.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(m.cookie)
		if err != nil || cookie.Value == "" {
			writeJSONError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess, err := m.db.GetSession(r.Context(), cookie.Value)
		if err != nil || sess == nil {
			writeJSONError(w, http.StatusUnauthorized, "session expired or invalid")
			return
		}
		user, err := m.db.GetUser(r.Context(), sess.GoogleID)
		if err != nil || user == nil {
			writeJSONError(w, http.StatusUnauthorized, "user not found")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey, user)
		ctx = context.WithValue(ctx, ctxSessionKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is HTTP middleware (chained after RequireAuth) that only lets
// admins through. is_admin is re-read from the database on every request, so
// removing someone's access takes effect straight away.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSONError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		if !user.IsAdmin {
			writeJSONError(w, http.StatusForbidden, "admins only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireLicensed is HTTP middleware (chained after RequireAuth) that
// additionally gates access on either admin status OR a valid license
// whose HWID matches the session's HWID. Unlicensed users get a 403
// with a structured JSON error the frontend can act on (showing the
// reset flow, etc).
func (m *Middleware) RequireLicensed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		sess := SessionFromContext(r.Context())
		if user == nil || sess == nil {
			writeJSONError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		// Admin bypass — set manually in db for the configured
		// ADMIN_EMAIL on first login. Admins skip the license check.
		if user.IsAdmin {
			next.ServeHTTP(w, r)
			return
		}
		lic, err := m.db.LicenseForUser(r.Context(), user.GoogleID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "license lookup failed")
			return
		}
		if lic == nil {
			writeJSONErrorCode(w, http.StatusForbidden, "no_license", "no license on this Google account")
			return
		}
		if !lic.IsActive() {
			writeJSONErrorCode(w, http.StatusForbidden, "license_inactive", "license is revoked or expired")
			return
		}
		// HWID check. Empty stored HWID = first activation; bind it now.
		if lic.HWID == "" {
			if err := m.db.BindLicenseHWID(r.Context(), lic.Key, sess.HWID); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "activation failed")
				return
			}
		} else if lic.HWID != sess.HWID {
			writeJSONErrorCode(w, http.StatusForbidden, "hwid_mismatch", "this license is bound to a different device")
			return
		}
		next.ServeHTTP(w, r)
	})
}
