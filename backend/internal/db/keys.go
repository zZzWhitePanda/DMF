package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrKeyUnavailable means a key doesn't exist, is used, revoked or expired.
// Callers don't get to tell which: that would help someone guessing keys.
var ErrKeyUnavailable = errors.New("key unavailable")

// migrate brings databases created by older builds up to schema.sql, which
// only adds what's missing. SQLite can't drop NOT NULL, so a licenses table
// that requires an owner is rebuilt. Unclaimed keys need that gone.
func migrate(ctx context.Context, s *sql.DB) error {
	var notNull int
	err := s.QueryRowContext(ctx, `SELECT "notnull" FROM pragma_table_info('licenses') WHERE name = 'google_id'`).Scan(&notNull)
	if err != nil {
		return err
	}
	if notNull == 0 {
		return nil
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_licenses_google_id`,
		`ALTER TABLE licenses RENAME TO licenses_old`,
		schemaSQL,
		`INSERT INTO licenses (key, google_id, hwid, last_reset_at, notes, created_at, expires_at, revoked_at, redeemed_at)
		 SELECT key, google_id, hwid, last_reset_at, notes, created_at, expires_at, revoked_at, created_at FROM licenses_old`,
		`DROP TABLE licenses_old`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild licenses: %w", err)
		}
	}
	return tx.Commit()
}

// KeyRow is one license as the admin console shows it.
type KeyRow struct {
	Key        string `json:"key"`
	Email      string `json:"email"` // who redeemed it, empty if unused
	DeviceSet  bool   `json:"device_set"`
	Notes      string `json:"notes"`
	PaidCents  int64  `json:"paid_cents"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	RedeemedAt int64  `json:"redeemed_at"`
	ExpiresAt  int64  `json:"expires_at"`
	RevokedAt  int64  `json:"revoked_at"`
}

// MintKey stores a new unclaimed key. expiresAt may be nil for no expiry.
func (d *DB) MintKey(ctx context.Context, key, notes string, paidCents int64, createdBy string, expiresAt *time.Time) error {
	var exp sql.NullInt64
	if expiresAt != nil {
		exp = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO licenses (key, notes, paid_cents, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, key, notes, paidCents, createdBy, time.Now().Unix(), exp)
	return err
}

// ClaimKey binds an unused key to a Google account. Only one claim can ever
// succeed, even if two people submit the same key at once.
func (d *DB) ClaimKey(ctx context.Context, key, googleID string) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx, `
		UPDATE licenses SET google_id = ?, redeemed_at = ?
		WHERE key = ? AND google_id IS NULL AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
	`, googleID, now, key, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrKeyUnavailable
	}
	return nil
}

// ListKeys returns every key, newest first. All admins see the same list.
func (d *DB) ListKeys(ctx context.Context) ([]KeyRow, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT l.key, COALESCE(u.email, ''), COALESCE(l.hwid, '') != '', COALESCE(l.notes, ''),
		       l.paid_cents, COALESCE(l.created_by, ''), l.created_at, COALESCE(l.redeemed_at, 0),
		       COALESCE(l.expires_at, 0), COALESCE(l.revoked_at, 0)
		FROM licenses l LEFT JOIN users u ON u.google_id = l.google_id
		ORDER BY l.created_at DESC, l.key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeyRow{}
	for rows.Next() {
		var k KeyRow
		if err := rows.Scan(&k.Key, &k.Email, &k.DeviceSet, &k.Notes, &k.PaidCents, &k.CreatedBy,
			&k.CreatedAt, &k.RedeemedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KeyOwner returns the Google ID a key is bound to. found is false for an
// unknown key, and an unused key returns an empty ID.
func (d *DB) KeyOwner(ctx context.Context, key string) (googleID string, found bool, err error) {
	var id sql.NullString
	err = d.sql.QueryRowContext(ctx, `SELECT google_id FROM licenses WHERE key = ?`, key).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id.String, true, nil
}

// UnrevokeLicense makes a revoked key usable again.
func (d *DB) UnrevokeLicense(ctx context.Context, key string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE licenses SET revoked_at = NULL WHERE key = ?`, key)
	return err
}

// ClearLicenseHWID frees a key from its device, so the next device to use it
// is bound with no cooldown.
func (d *DB) ClearLicenseHWID(ctx context.Context, key string) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE licenses SET hwid = NULL, last_reset_at = 0 WHERE key = ?`, key)
	return err
}

// AdminRow is one admin as the console shows it.
type AdminRow struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// ListAdmins returns every user with admin access.
func (d *DB) ListAdmins(ctx context.Context) ([]AdminRow, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT email, COALESCE(name, '') FROM users WHERE is_admin = 1 ORDER BY LOWER(email)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminRow{}
	for rows.Next() {
		var a AdminRow
		if err := rows.Scan(&a.Email, &a.Name); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAdminByEmail flips admin access. It reports false if nobody with that
// email has signed in yet.
func (d *DB) SetAdminByEmail(ctx context.Context, email string, isAdmin bool) (bool, error) {
	res, err := d.sql.ExecContext(ctx, `
		UPDATE users SET is_admin = ? WHERE LOWER(email) = LOWER(?)
	`, boolToInt(isAdmin), email)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
