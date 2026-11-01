-- Memeforge database schema.
--
-- SQLite over a Fly volume mount. The volume is persistent across deploys
-- so license + account state survives container restarts. If we ever
-- outgrow SQLite we can swap to Fly Postgres with a schema migration.

-- users: everyone who has logged in with Google. Created on first OAuth
-- callback. Email is cached for display; the Google ID is authoritative.
CREATE TABLE IF NOT EXISTS users (
    google_id       TEXT PRIMARY KEY,
    email           TEXT NOT NULL,
    name            TEXT,
    picture_url     TEXT,
    is_admin        INTEGER NOT NULL DEFAULT 0,     -- 1 = admin bypass (set manually for dev)
    created_at      INTEGER NOT NULL,                -- unix seconds
    last_login_at   INTEGER NOT NULL
);

-- licenses: minted via the admin key-gen site. One license per key, bound
-- to a specific Google ID. The license tracks the HWID the user activated
-- it on; mismatches trigger the 24h-cooldown reset flow.
CREATE TABLE IF NOT EXISTS licenses (
    key              TEXT PRIMARY KEY,
    google_id        TEXT NOT NULL,                   -- bound Google account
    hwid             TEXT,                            -- device fingerprint; NULL = never activated
    last_reset_at    INTEGER NOT NULL DEFAULT 0,      -- unix seconds of last HWID reset (0 = never)
    notes            TEXT,                            -- admin-visible notes (who, why, when)
    created_at       INTEGER NOT NULL,
    expires_at       INTEGER,                         -- NULL = no expiry
    revoked_at       INTEGER,                         -- NULL = active
    FOREIGN KEY (google_id) REFERENCES users(google_id)
);

CREATE INDEX IF NOT EXISTS idx_licenses_google_id ON licenses(google_id);

-- discord_accounts: a user's Discord selfbot accounts to grind. One user
-- can have many accounts (the current cluster concept, but managed per-
-- user). Config is stored as a yaml blob (same shape as config.yaml) so
-- the grinder's existing Load() works on it unchanged.
CREATE TABLE IF NOT EXISTS discord_accounts (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    google_id        TEXT NOT NULL,                   -- owning user
    name             TEXT NOT NULL,                   -- display label ("alpha", "my fish account", etc)
    config_yaml      TEXT NOT NULL,                   -- grinder config.yaml contents
    is_running       INTEGER NOT NULL DEFAULT 0,      -- 1 = grinder is currently active for this account
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    FOREIGN KEY (google_id) REFERENCES users(google_id)
);

CREATE INDEX IF NOT EXISTS idx_accounts_google_id ON discord_accounts(google_id);

-- sessions: logged-in browser sessions. Cookie-based. The cookie carries
-- the session_id; the server looks up the google_id + expiry here.
CREATE TABLE IF NOT EXISTS sessions (
    id               TEXT PRIMARY KEY,                -- random session id (also the cookie value)
    google_id        TEXT NOT NULL,
    hwid             TEXT,                            -- captured on login for license match
    expires_at       INTEGER NOT NULL,
    created_at       INTEGER NOT NULL,
    FOREIGN KEY (google_id) REFERENCES users(google_id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
