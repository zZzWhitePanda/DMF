package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A database from before unclaimed keys must keep its licenses through the
// upgrade, and run the upgrade only once.
func TestMigrateKeepsOldLicenses(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	dsn := "file:" + path + "?_pragma=foreign_keys(1)"
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users (google_id TEXT PRIMARY KEY, email TEXT NOT NULL, name TEXT, picture_url TEXT,
			is_admin INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, last_login_at INTEGER NOT NULL)`,
		`CREATE TABLE licenses (key TEXT PRIMARY KEY, google_id TEXT NOT NULL, hwid TEXT,
			last_reset_at INTEGER NOT NULL DEFAULT 0, notes TEXT, created_at INTEGER NOT NULL,
			expires_at INTEGER, revoked_at INTEGER, FOREIGN KEY (google_id) REFERENCES users(google_id))`,
		`CREATE INDEX idx_licenses_google_id ON licenses(google_id)`,
		`INSERT INTO users VALUES ('u', 'u@example.com', '', '', 0, 1, 1)`,
		`INSERT INTO licenses (key, google_id, hwid, notes, created_at) VALUES ('OLD', 'u', 'dev', 'kept', 5)`,
	} {
		if _, err := s.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	for run := 0; run < 2; run++ {
		d, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", run, err)
		}
		lic, err := d.LicenseForUser(ctx, "u")
		if err != nil || lic == nil || lic.Key != "OLD" || lic.HWID != "dev" || lic.Notes != "kept" {
			t.Fatalf("license after migrate: %+v, %v", lic, err)
		}
		if err := d.MintKey(ctx, "NEW"+string(rune('0'+run)), "", 0, "", nil); err != nil {
			t.Fatalf("unclaimed key after migrate: %v", err)
		}
		d.Close()
	}
}
