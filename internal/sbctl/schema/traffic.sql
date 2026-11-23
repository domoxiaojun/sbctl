CREATE TABLE IF NOT EXISTS snapshots (
 node_id TEXT PRIMARY KEY, ledger_id TEXT NOT NULL, seq INTEGER NOT NULL,
 checksum TEXT NOT NULL, document TEXT NOT NULL, synced_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS observations (
 node_id TEXT PRIMARY KEY, error_code TEXT, checked_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ledger_replacements (
 node_id TEXT NOT NULL, ledger_id TEXT NOT NULL, replaced_by TEXT NOT NULL,
 document TEXT NOT NULL, registered_at TEXT NOT NULL,
 PRIMARY KEY(node_id,ledger_id), UNIQUE(node_id,replaced_by)
);
CREATE TABLE IF NOT EXISTS history_nodes (
 node_id TEXT PRIMARY KEY, started_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS history_days (
 node_id TEXT NOT NULL, day TEXT NOT NULL, document TEXT NOT NULL, dirty INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(node_id, day)
);
CREATE TABLE IF NOT EXISTS history_hours (
 node_id TEXT NOT NULL, hour TEXT NOT NULL, document TEXT NOT NULL, dirty INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(node_id, hour)
);
CREATE TABLE IF NOT EXISTS connections (
 node_id TEXT NOT NULL, connection_id TEXT NOT NULL, inbound_id TEXT NOT NULL DEFAULT '', inbound_tag TEXT NOT NULL DEFAULT '',
 inbound_type TEXT NOT NULL DEFAULT '', network TEXT NOT NULL DEFAULT '', domain TEXT NOT NULL DEFAULT '',
 destination TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '', outbound TEXT NOT NULL DEFAULT '',
 outbound_type TEXT NOT NULL DEFAULT '', chain_json TEXT NOT NULL DEFAULT '[]', rule TEXT NOT NULL DEFAULT '',
 upload INTEGER NOT NULL DEFAULT 0, download INTEGER NOT NULL DEFAULT 0,
 started_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT '', observed_at TEXT NOT NULL,
 PRIMARY KEY(node_id, connection_id)
);
CREATE INDEX IF NOT EXISTS traffic_connections_started ON connections(started_at);
CREATE INDEX IF NOT EXISTS traffic_connections_node_started ON connections(node_id,started_at DESC);
CREATE INDEX IF NOT EXISTS traffic_connections_domain ON connections(started_at,domain);
CREATE INDEX IF NOT EXISTS traffic_connections_outbound ON connections(started_at,outbound);
CREATE INDEX IF NOT EXISTS traffic_connections_rule ON connections(started_at,rule);
CREATE TABLE IF NOT EXISTS connection_cursors (
 node_id TEXT PRIMARY KEY, ledger_id TEXT NOT NULL, seq INTEGER NOT NULL,
 history_gap INTEGER NOT NULL DEFAULT 0
);
