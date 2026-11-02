// Package db wraps a SQLite connection pool and typed queries for the
// Dank Memer Farm backend. Uses modernc.org/sqlite — a pure-Go port with no
// CGO requirement, so the Fly.io Dockerfile stays tiny and deploys fast.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB is the backend's database handle.
type DB struct {
	sql *sql.DB
}

// Open opens or creates the SQLite file at path, applies the schema
// idempotently, and returns a wrapped handle. On Fly.io, path should
// live on a mounted volume so the data persists across deploys.
func Open(path string) (*DB, error) {
	// _journal=WAL + _busy_timeout keeps writes smooth under light load
	// from the HTTP handlers + grinder goroutines.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := s.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := s.ExecContext(context.Background(), schemaSQL); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(context.Background(), s); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{sql: s}, nil
}

// Close releases the underlying connection pool.
func (d *DB) Close() error { return d.sql.Close() }

// ---------------------------------------------------------------------------
// users
// ---------------------------------------------------------------------------

type User struct {
	GoogleID    string
	Email       string
	Name        string
	PictureURL  string
	IsAdmin     bool
	CreatedAt   time.Time
	LastLoginAt time.Time
}

// UpsertUser inserts a new user record or refreshes the existing one's
// email/name/picture + last_login_at. Called on every successful Google
// OAuth callback.
func (d *DB) UpsertUser(ctx context.Context, u User) error {
	now := time.Now().Unix()
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO users (google_id, email, name, picture_url, is_admin, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(google_id) DO UPDATE SET
			email = excluded.email,
			name = excluded.name,
			picture_url = excluded.picture_url,
			last_login_at = excluded.last_login_at
	`, u.GoogleID, u.Email, u.Name, u.PictureURL, boolToInt(u.IsAdmin), now, now)
	return err
}

// GetUser fetches a user by Google ID. Returns (nil, nil) if not found.
func (d *DB) GetUser(ctx context.Context, googleID string) (*User, error) {
	var u User
	var createdAt, lastLoginAt int64
	var isAdmin int
	err := d.sql.QueryRowContext(ctx, `
		SELECT google_id, email, COALESCE(name, ''), COALESCE(picture_url, ''),
		       is_admin, created_at, last_login_at
		FROM users WHERE google_id = ?
	`, googleID).Scan(&u.GoogleID, &u.Email, &u.Name, &u.PictureURL, &isAdmin, &createdAt, &lastLoginAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	u.CreatedAt = time.Unix(createdAt, 0)
	u.LastLoginAt = time.Unix(lastLoginAt, 0)
	return &u, nil
}

// SetAdmin flips the is_admin flag for a user. Used by the dev bootstrap
// to grant the configured admin email access without a license.
func (d *DB) SetAdmin(ctx context.Context, googleID string, isAdmin bool) error {
	_, err := d.sql.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE google_id = ?`, boolToInt(isAdmin), googleID)
	return err
}

// ---------------------------------------------------------------------------
// licenses
// ---------------------------------------------------------------------------

type License struct {
	Key          string
	GoogleID     string
	HWID         string
	LastResetAt  time.Time
	Notes        string
	CreatedAt    time.Time
	ExpiresAt    *time.Time // nil = no expiry
	RevokedAt    *time.Time // nil = active
}

// IsActive reports whether a license is currently usable: not revoked,
// not expired.
func (l *License) IsActive() bool {
	if l.RevokedAt != nil {
		return false
	}
	if l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt) {
		return false
	}
	return true
}

// LicenseForUser returns the active license bound to a Google ID, or
// (nil, nil) if none exists.
func (d *DB) LicenseForUser(ctx context.Context, googleID string) (*License, error) {
	var l License
	var hwid sql.NullString
	var createdAt, lastResetAt int64
	var expiresAt, revokedAt sql.NullInt64
	err := d.sql.QueryRowContext(ctx, `
		SELECT key, google_id, COALESCE(hwid, ''), last_reset_at, COALESCE(notes, ''),
		       created_at, expires_at, revoked_at
		FROM licenses WHERE google_id = ? AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, googleID).Scan(&l.Key, &l.GoogleID, &hwid, &lastResetAt, &l.Notes, &createdAt, &expiresAt, &revokedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.HWID = hwid.String
	l.CreatedAt = time.Unix(createdAt, 0)
	l.LastResetAt = time.Unix(lastResetAt, 0)
	if expiresAt.Valid {
		t := time.Unix(expiresAt.Int64, 0)
		l.ExpiresAt = &t
	}
	if revokedAt.Valid {
		t := time.Unix(revokedAt.Int64, 0)
		l.RevokedAt = &t
	}
	return &l, nil
}

// BindLicenseHWID stores a device fingerprint on a license (first
// activation) OR replaces the stored one (reset flow). Updates
// last_reset_at so the 24h cooldown gate works.
func (d *DB) BindLicenseHWID(ctx context.Context, key, hwid string) error {
	_, err := d.sql.ExecContext(ctx, `
		UPDATE licenses SET hwid = ?, last_reset_at = ? WHERE key = ?
	`, hwid, time.Now().Unix(), key)
	return err
}

// MintLicense inserts a new license bound to a Google ID. Called by the
// admin key-gen site. expiresAt may be nil for no-expiry licenses.
func (d *DB) MintLicense(ctx context.Context, key, googleID, notes string, expiresAt *time.Time) error {
	var exp sql.NullInt64
	if expiresAt != nil {
		exp = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO licenses (key, google_id, notes, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, key, googleID, notes, time.Now().Unix(), exp)
	return err
}

// RevokeLicense marks a license revoked. Does not delete so the record
// remains auditable.
func (d *DB) RevokeLicense(ctx context.Context, key string) error {
	_, err := d.sql.ExecContext(ctx, `
		UPDATE licenses SET revoked_at = ? WHERE key = ? AND revoked_at IS NULL
	`, time.Now().Unix(), key)
	return err
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

type Session struct {
	ID        string
	GoogleID  string
	HWID      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateSession stores a browser session. The returned ID is set as the
// cookie value.
func (d *DB) CreateSession(ctx context.Context, s Session) error {
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO sessions (id, google_id, hwid, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, s.ID, s.GoogleID, s.HWID, s.ExpiresAt.Unix(), s.CreatedAt.Unix())
	return err
}

// GetSession looks up a session by ID. Returns (nil, nil) if not found
// or expired.
func (d *DB) GetSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	var expiresAt, createdAt int64
	var hwid sql.NullString
	err := d.sql.QueryRowContext(ctx, `
		SELECT id, google_id, COALESCE(hwid, ''), expires_at, created_at
		FROM sessions WHERE id = ? AND expires_at > ?
	`, id, time.Now().Unix()).Scan(&s.ID, &s.GoogleID, &hwid, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.HWID = hwid.String
	s.ExpiresAt = time.Unix(expiresAt, 0)
	s.CreatedAt = time.Unix(createdAt, 0)
	return &s, nil
}

// DeleteSession removes a session. Used on logout.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PurgeExpiredSessions deletes any sessions past their expiry. Run
// periodically from a background goroutine.
func (d *DB) PurgeExpiredSessions(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

// ---------------------------------------------------------------------------
// discord_accounts
// ---------------------------------------------------------------------------

type DiscordAccount struct {
	ID         int64
	GoogleID   string
	Name       string
	ConfigYAML string
	IsRunning  bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListAccounts returns all Discord accounts owned by a user, newest first.
func (d *DB) ListAccounts(ctx context.Context, googleID string) ([]DiscordAccount, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT id, google_id, name, config_yaml, is_running, created_at, updated_at
		FROM discord_accounts WHERE google_id = ?
		ORDER BY created_at DESC
	`, googleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscordAccount
	for rows.Next() {
		var a DiscordAccount
		var isRunning int
		var createdAt, updatedAt int64
		if err := rows.Scan(&a.ID, &a.GoogleID, &a.Name, &a.ConfigYAML, &isRunning, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		a.IsRunning = isRunning == 1
		a.CreatedAt = time.Unix(createdAt, 0)
		a.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount fetches one Discord account by ID, scoped to a user.
func (d *DB) GetAccount(ctx context.Context, id int64, googleID string) (*DiscordAccount, error) {
	var a DiscordAccount
	var isRunning int
	var createdAt, updatedAt int64
	err := d.sql.QueryRowContext(ctx, `
		SELECT id, google_id, name, config_yaml, is_running, created_at, updated_at
		FROM discord_accounts WHERE id = ? AND google_id = ?
	`, id, googleID).Scan(&a.ID, &a.GoogleID, &a.Name, &a.ConfigYAML, &isRunning, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.IsRunning = isRunning == 1
	a.CreatedAt = time.Unix(createdAt, 0)
	a.UpdatedAt = time.Unix(updatedAt, 0)
	return &a, nil
}

// CreateAccount inserts a new Discord account. Returns the new ID.
func (d *DB) CreateAccount(ctx context.Context, a DiscordAccount) (int64, error) {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx, `
		INSERT INTO discord_accounts (google_id, name, config_yaml, is_running, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?)
	`, a.GoogleID, a.Name, a.ConfigYAML, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAccountConfig overwrites the stored yaml for an account.
func (d *DB) UpdateAccountConfig(ctx context.Context, id int64, googleID, configYAML string) error {
	_, err := d.sql.ExecContext(ctx, `
		UPDATE discord_accounts SET config_yaml = ?, updated_at = ?
		WHERE id = ? AND google_id = ?
	`, configYAML, time.Now().Unix(), id, googleID)
	return err
}

// SetAccountRunning flips the is_running flag.
func (d *DB) SetAccountRunning(ctx context.Context, id int64, running bool) error {
	_, err := d.sql.ExecContext(ctx, `
		UPDATE discord_accounts SET is_running = ?, updated_at = ? WHERE id = ?
	`, boolToInt(running), time.Now().Unix(), id)
	return err
}

// DeleteAccount removes an account. The runtime manager must have already
// stopped it.
func (d *DB) DeleteAccount(ctx context.Context, id int64, googleID string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM discord_accounts WHERE id = ? AND google_id = ?`, id, googleID)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
