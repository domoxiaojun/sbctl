CREATE TABLE IF NOT EXISTS requests (
 id TEXT PRIMARY KEY, revision INTEGER UNIQUE NOT NULL, payload_hash TEXT NOT NULL,
 document TEXT NOT NULL, state TEXT NOT NULL, accepted_at TEXT NOT NULL,
 applied_at TEXT, verified_at TEXT, config_hash TEXT, error_code TEXT,
 previous_epoch TEXT, applied_epoch TEXT
);
CREATE TABLE IF NOT EXISTS revoked (fingerprint TEXT PRIMARY KEY);
