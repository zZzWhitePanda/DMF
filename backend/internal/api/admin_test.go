package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/auth"
	"github.com/zZzWhitePanda/DMF/backend/internal/db"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
)

type adminServer struct {
	*farmServer
	admin http.Handler
	lic   http.Handler
}

// newAdminServer signs in owner (ADMIN_EMAIL), helper (a granted admin),
// buyer and thief alongside the farm server's licensed user.
func newAdminServer(t *testing.T) *adminServer {
	t.Helper()
	s := newFarmServer(t)
	for _, id := range []string{"owner", "helper", "buyer", "thief"} {
		s.addUser(id)
	}
	ctx := context.Background()
	for _, id := range []string{"owner", "helper"} {
		if err := s.db.SetAdmin(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	mw := auth.NewMiddleware(auth.NewGoogleProvider(auth.Config{SessionCookie: testCookie}), s.db)
	adminMux, licMux := http.NewServeMux(), http.NewServeMux()
	NewAdminHandlers(s.db, s.manager, "owner@example.com").Register(adminMux)
	NewLicenseHandlers(s.db).Register(licMux)
	return &adminServer{s, mw.RequireAuth(mw.RequireAdmin(adminMux)), mw.RequireAuth(licMux)}
}

func (s *adminServer) call(h http.Handler, session, method, path, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if session != "" {
		r.AddCookie(&http.Cookie{Name: testCookie, Value: session})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (s *adminServer) mint(session, body string) []string {
	s.t.Helper()
	w := s.call(s.admin, session, http.MethodPost, "/api/admin/keys", body)
	if w.Code != http.StatusOK {
		s.t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	var out struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		s.t.Fatal(err)
	}
	return out.Keys
}

func (s *adminServer) redeem(session, key string) int {
	s.t.Helper()
	return s.call(s.lic, session, http.MethodPost, "/api/license/redeem", `{"key":"`+key+`"}`).Code
}

func (s *adminServer) listKeys(session string) []db.KeyRow {
	s.t.Helper()
	w := s.call(s.admin, session, http.MethodGet, "/api/admin/keys", "")
	if w.Code != http.StatusOK {
		s.t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var out struct {
		Keys []db.KeyRow `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		s.t.Fatal(err)
	}
	return out.Keys
}

func (s *adminServer) keyRow(session, key string) db.KeyRow {
	s.t.Helper()
	for _, k := range s.listKeys(session) {
		if k.Key == key {
			return k
		}
	}
	s.t.Fatalf("key %s not listed", key)
	return db.KeyRow{}
}

func TestAdminRoutesAreForAdminsOnly(t *testing.T) {
	s := newAdminServer(t)
	for _, session := range []string{"", "buyer", "licensed"} {
		want := http.StatusForbidden
		if session == "" {
			want = http.StatusUnauthorized
		}
		if got := s.call(s.admin, session, http.MethodGet, "/api/admin/keys", "").Code; got != want {
			t.Fatalf("list as %q: %d, want %d", session, got, want)
		}
		if got := s.call(s.admin, session, http.MethodPost, "/api/admin/keys", `{"count":1}`).Code; got != want {
			t.Fatalf("mint as %q: %d, want %d", session, got, want)
		}
	}
}

func TestMintedKeysAreSharedBetweenAdmins(t *testing.T) {
	s := newAdminServer(t)
	keys := s.mint("helper", `{"count":3,"paid_cents":9000,"notes":"  bulk  ","expires_days":30}`)
	if len(keys) != 3 {
		t.Fatalf("got %d keys", len(keys))
	}
	format := regexp.MustCompile(`^DMF(-[A-HJ-NP-Z2-9]{4}){4}$`)
	seen := map[string]bool{}
	for _, k := range keys {
		if !format.MatchString(k) || seen[k] {
			t.Fatalf("bad or repeated key %q", k)
		}
		seen[k] = true
	}
	row := s.keyRow("owner", keys[0])
	if row.PaidCents != 3000 || row.Notes != "bulk" || row.CreatedBy != "helper@example.com" {
		t.Fatalf("got %+v", row)
	}
	if row.ExpiresAt < time.Now().AddDate(0, 0, 29).Unix() {
		t.Fatalf("expires %d", row.ExpiresAt)
	}
}

func TestMintRejectsBadInput(t *testing.T) {
	s := newAdminServer(t)
	for _, body := range []string{`{"count":0}`, `{"count":501}`, `{"count":1,"paid_cents":-1}`, `not json`} {
		if got := s.call(s.admin, "owner", http.MethodPost, "/api/admin/keys", body).Code; got != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, got)
		}
	}
}

func TestKeyWorksForOneAccountOnly(t *testing.T) {
	s := newAdminServer(t)
	key := s.mint("owner", `{"count":1}`)[0]

	if got := s.do("buyer", http.MethodGet, "/api/farm", "").Code; got != http.StatusForbidden {
		t.Fatalf("before redeem: %d", got)
	}
	if got := s.redeem("buyer", strings.ToLower(key)); got != http.StatusOK {
		t.Fatalf("redeem: %d", got)
	}
	if got := s.do("buyer", http.MethodGet, "/api/farm", "").Code; got != http.StatusOK {
		t.Fatalf("after redeem: %d", got)
	}
	if got := s.redeem("thief", key); got != http.StatusNotFound {
		t.Fatalf("second account: %d", got)
	}
	if got := s.redeem("thief", "DMF-NOPE-NOPE-NOPE-NOPE"); got != http.StatusNotFound {
		t.Fatalf("made-up key: %d", got)
	}
	if got := s.redeem("buyer", s.mint("owner", `{"count":1}`)[0]); got != http.StatusConflict {
		t.Fatalf("second key on one account: %d", got)
	}
	row := s.keyRow("owner", key)
	if row.Email != "buyer@example.com" || row.RedeemedAt == 0 || !row.DeviceSet {
		t.Fatalf("got %+v", row)
	}
}

func TestRevokedOrExpiredKeysCantBeRedeemed(t *testing.T) {
	s := newAdminServer(t)
	revoked := s.mint("owner", `{"count":1}`)[0]
	s.call(s.admin, "owner", http.MethodPost, "/api/admin/keys/revoke", `{"key":"`+revoked+`"}`)
	if got := s.redeem("buyer", revoked); got != http.StatusNotFound {
		t.Fatalf("revoked: %d", got)
	}
	s.call(s.admin, "owner", http.MethodPost, "/api/admin/keys/unrevoke", `{"key":"`+revoked+`"}`)
	if got := s.redeem("buyer", revoked); got != http.StatusOK {
		t.Fatalf("unrevoked: %d", got)
	}

	past := time.Now().Add(-time.Hour)
	if err := s.db.MintKey(context.Background(), "DMF-OLD", "", 0, "", &past); err != nil {
		t.Fatal(err)
	}
	if got := s.redeem("thief", "DMF-OLD"); got != http.StatusNotFound {
		t.Fatalf("expired: %d", got)
	}
}

func TestRevokeLocksTheBuyerOutAndStopsTheirFarm(t *testing.T) {
	s := newAdminServer(t)
	key := s.mint("owner", `{"count":1}`)[0]
	s.redeem("buyer", key)
	ctx := context.Background()
	account, err := s.db.CreateAccount(ctx, db.DiscordAccount{GoogleID: "buyer", Name: "theirs", ConfigYAML: defaultConfigYAML})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.manager.Start(account, &config.Config{}); err != nil {
		t.Fatal(err)
	}

	if got := s.call(s.admin, "helper", http.MethodPost, "/api/admin/keys/revoke", `{"key":"`+key+`"}`).Code; got != http.StatusOK {
		t.Fatalf("revoke: %d", got)
	}
	if s.manager.IsRunning(account) {
		t.Fatal("farm still running after revoke")
	}
	if got := s.do("buyer", http.MethodGet, "/api/farm", "").Code; got != http.StatusForbidden {
		t.Fatalf("after revoke: %d", got)
	}
	if got := s.call(s.admin, "owner", http.MethodPost, "/api/admin/keys/revoke", `{"key":"DMF-NONE"}`).Code; got != http.StatusNotFound {
		t.Fatalf("unknown key: %d", got)
	}
}

func TestResetDeviceFreesTheKey(t *testing.T) {
	s := newAdminServer(t)
	key := s.mint("owner", `{"count":1}`)[0]
	s.redeem("buyer", key)
	s.do("buyer", http.MethodGet, "/api/farm", "")
	if !s.keyRow("owner", key).DeviceSet {
		t.Fatal("device not bound on first use")
	}
	s.call(s.admin, "owner", http.MethodPost, "/api/admin/keys/reset-device", `{"key":"`+key+`"}`)
	if s.keyRow("owner", key).DeviceSet {
		t.Fatal("device still bound")
	}
}

func TestOnlyTheOwnerChangesAdmins(t *testing.T) {
	s := newAdminServer(t)
	post := func(session, body string) int {
		return s.call(s.admin, session, http.MethodPost, "/api/admin/admins", body).Code
	}
	if got := post("helper", `{"email":"buyer@example.com","admin":true}`); got != http.StatusForbidden {
		t.Fatalf("helper granting: %d", got)
	}
	if got := post("owner", `{"email":"BUYER@example.com","admin":true}`); got != http.StatusOK {
		t.Fatalf("owner granting: %d", got)
	}
	if got := s.call(s.admin, "buyer", http.MethodGet, "/api/admin/keys", "").Code; got != http.StatusOK {
		t.Fatalf("new admin listing: %d", got)
	}
	if got := post("owner", `{"email":"buyer@example.com","admin":false}`); got != http.StatusOK {
		t.Fatalf("owner removing: %d", got)
	}
	if got := s.call(s.admin, "buyer", http.MethodGet, "/api/admin/keys", "").Code; got != http.StatusForbidden {
		t.Fatalf("removed admin listing: %d", got)
	}
	if got := post("owner", `{"email":"owner@example.com","admin":false}`); got != http.StatusBadRequest {
		t.Fatalf("removing the owner: %d", got)
	}
	if got := post("owner", `{"email":"nobody@example.com","admin":true}`); got != http.StatusNotFound {
		t.Fatalf("unknown email: %d", got)
	}
}
