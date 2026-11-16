CREATE TABLE IF NOT EXISTS snapshots (
 node_id TEXT PRIMARY KEY, ledger_id TEXT NOT NULL, seq INTEGER NOT NULL,
 checksum TEXT NOT NULL, document TEXT NOT NULL, synced_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS observations (
 node_id TEXT PRIMARY KEY, error_code TEXT, checked_at TEXT NOT NULL
);
