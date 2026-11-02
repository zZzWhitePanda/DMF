package api

import (
	"net/http"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
)

// LicenseHandlers exposes license-management endpoints that run BEHIND
// RequireAuth but NOT RequireLicensed — the whole point of the reset
// endpoint is for users who can't pass the license check yet.
type LicenseHandlers struct {
	db *db.DB
}

func NewLicenseHandlers(d *db.DB) *LicenseHandlers {
	return &LicenseHandlers{db: d}
}

func (h *LicenseHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/license/reset-hwid", h.resetHWID)
	mux.HandleFunc("/api/license/redeem", h.redeem)
}

// resetHWID rebinds the current user's license to the current browser's
// HWID. Gated to one call per 24 hours.
func (h *LicenseHandlers) resetHWID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := auth.UserFromContext(r.Context())
	sess := auth.SessionFromContext(r.Context())
	if user == nil || sess == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if user.IsAdmin {
		// Admins don't need licenses — nothing to reset.
		writeJSON(w, http.StatusOK, map[string]string{"status": "admin"})
		return
	}
	lic, err := h.db.LicenseForUser(r.Context(), user.GoogleID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if lic == nil {
		writeError(w, http.StatusForbidden, "no license on account")
		return
	}
	if !lic.IsActive() {
		writeError(w, http.StatusForbidden, "license inactive")
		return
	}
	// First-time binding has LastResetAt == epoch zero — allow freely.
	// Subsequent resets gate on 24h since last reset.
	if !lic.LastResetAt.IsZero() && lic.LastResetAt.Unix() > 0 {
		if time.Since(lic.LastResetAt) < 24*time.Hour {
			remaining := (24 * time.Hour) - time.Since(lic.LastResetAt)
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"error":           "reset cooldown active",
				"code":            "reset_cooldown",
				"available_in_s":  int(remaining.Seconds()),
			})
			return
		}
	}
	if err := h.db.BindLicenseHWID(r.Context(), lic.Key, sess.HWID); err != nil {
		writeError(w, http.StatusInternalServerError, "rebind failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
