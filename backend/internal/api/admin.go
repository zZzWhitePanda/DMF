package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/runtime"
	log "github.com/sirupsen/logrus"
)

const maxMint = 500

// keyAlphabet leaves out 0, O, 1 and I so a key survives being read out.
const keyAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newKey returns a key like DMF-XXXX-XXXX-XXXX-XXXX, 80 random bits in all.
func newKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("DMF")
	for i, c := range raw {
		if i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(keyAlphabet[int(c)%len(keyAlphabet)])
	}
	return b.String(), nil
}

// AdminHandlers is the key and admin console API. Every route runs behind
// RequireAdmin. Changing who is an admin takes the owner, the ADMIN_EMAIL
// account, so an admin can't add more admins or lock the owner out.
type AdminHandlers struct {
	db      *db.DB
	manager *runtime.Manager
	owner   string
}

func NewAdminHandlers(d *db.DB, m *runtime.Manager, ownerEmail string) *AdminHandlers {
	return &AdminHandlers{db: d, manager: m, owner: ownerEmail}
}

func (h *AdminHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/keys", h.keys)
	mux.HandleFunc("/api/admin/keys/revoke", h.keyAction(h.revoke))
	mux.HandleFunc("/api/admin/keys/unrevoke", h.keyAction(h.db.UnrevokeLicense))
	mux.HandleFunc("/api/admin/keys/reset-device", h.keyAction(h.db.ClearLicenseHWID))
	mux.HandleFunc("/api/admin/admins", h.admins)
}

func (h *AdminHandlers) isOwner(u *db.User) bool {
	return strings.EqualFold(u.Email, h.owner)
}

func (h *AdminHandlers) keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.ListKeys(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key lookup failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"keys": keys})
	case http.MethodPost:
		h.mint(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mint creates count unused keys. paid_cents is the total for the batch.
func (h *AdminHandlers) mint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count       int    `json:"count"`
		PaidCents   int64  `json:"paid_cents"`
		Notes       string `json:"notes"`
		ExpiresDays int    `json:"expires_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Count < 1 || body.Count > maxMint {
		writeError(w, http.StatusBadRequest, "count must be 1 to 500")
		return
	}
	if body.PaidCents < 0 || body.ExpiresDays < 0 {
		writeError(w, http.StatusBadRequest, "amounts can't be negative")
		return
	}
	var expires *time.Time
	if body.ExpiresDays > 0 {
		t := time.Now().AddDate(0, 0, body.ExpiresDays)
		expires = &t
	}
	creator := auth.UserFromContext(r.Context()).Email
	each := body.PaidCents / int64(body.Count)
	minted := make([]string, 0, body.Count)
	for n := 0; n < body.Count; n++ {
		key, err := newKey()
		if err == nil {
			err = h.db.MintKey(r.Context(), key, strings.TrimSpace(body.Notes), each, creator, expires)
		}
		if err != nil {
			log.WithError(err).Warn("mint failed")
			writeError(w, http.StatusInternalServerError, "mint failed")
			return
		}
		minted = append(minted, key)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": minted})
}

// keyAction wraps a change to one existing key.
func (h *AdminHandlers) keyAction(apply func(context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		_, found, err := h.db.KeyOwner(r.Context(), body.Key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key lookup failed")
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "no such key")
			return
		}
		if err := apply(r.Context(), body.Key); err != nil {
			writeError(w, http.StatusInternalServerError, "update failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// revoke also stops the owner's farm. The license check only runs on requests,
// so a running grinder would otherwise carry on with no license.
func (h *AdminHandlers) revoke(ctx context.Context, key string) error {
	owner, _, err := h.db.KeyOwner(ctx, key)
	if err != nil {
		return err
	}
	if err := h.db.RevokeLicense(ctx, key); err != nil {
		return err
	}
	if owner == "" {
		return nil
	}
	accounts, err := h.db.ListAccounts(ctx, owner)
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		h.manager.Stop(acc.ID)
		if err := h.db.SetAccountRunning(ctx, acc.ID, false); err != nil {
			return err
		}
	}
	return nil
}

func (h *AdminHandlers) admins(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		admins, err := h.db.ListAdmins(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "admin lookup failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"admins": admins, "owner": h.owner})
	case http.MethodPost:
		h.setAdmin(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *AdminHandlers) setAdmin(w http.ResponseWriter, r *http.Request) {
	if !h.isOwner(auth.UserFromContext(r.Context())) {
		writeError(w, http.StatusForbidden, "only the owner can change admins")
		return
	}
	var body struct {
		Email string `json:"email"`
		Admin bool   `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	email := strings.TrimSpace(body.Email)
	if strings.EqualFold(email, h.owner) {
		writeError(w, http.StatusBadRequest, "the owner is always an admin")
		return
	}
	found, err := h.db.SetAdminByEmail(r.Context(), email, body.Admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "they need to sign in to the site once first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// redeem binds a key to the signed-in account. The device is bound by the
// license check on the first farm request after this.
func (h *LicenseHandlers) redeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if existing, err := h.db.LicenseForUser(r.Context(), user.GoogleID); err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	} else if existing != nil && existing.IsActive() {
		writeError(w, http.StatusConflict, "this account already has a license")
		return
	}
	key := strings.ToUpper(strings.TrimSpace(body.Key))
	switch err := h.db.ClaimKey(r.Context(), key, user.GoogleID); {
	case errors.Is(err, db.ErrKeyUnavailable):
		writeError(w, http.StatusNotFound, "that key isn't valid or has already been used")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "redeem failed")
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
