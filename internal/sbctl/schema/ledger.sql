CREATE TABLE IF NOT EXISTS series_totals (
 scope TEXT NOT NULL, key TEXT NOT NULL, protocol TEXT NOT NULL, port INTEGER NOT NULL,
 upload INTEGER NOT NULL, download INTEGER NOT NULL, started_at TEXT NOT NULL, active INTEGER NOT NULL,
 tag TEXT NOT NULL DEFAULT '', history_tag TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(scope,key)
);
CREATE TABLE IF NOT EXISTS series_cursors (
 scope TEXT NOT NULL, key TEXT NOT NULL, direction TEXT NOT NULL, epoch TEXT NOT NULL,
 value INTEGER NOT NULL, seen INTEGER NOT NULL, PRIMARY KEY(scope,key,direction,epoch)
);
CREATE TABLE IF NOT EXISTS connections (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, connection_id TEXT NOT NULL UNIQUE, inbound_id TEXT NOT NULL DEFAULT '', inbound_tag TEXT NOT NULL DEFAULT '',
 inbound_type TEXT NOT NULL DEFAULT '', network TEXT NOT NULL DEFAULT '', domain TEXT NOT NULL DEFAULT '',
 destination TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '', outbound TEXT NOT NULL DEFAULT '',
 outbound_type TEXT NOT NULL DEFAULT '', chain_json TEXT NOT NULL DEFAULT '[]', rule TEXT NOT NULL DEFAULT '',
 upload INTEGER NOT NULL DEFAULT 0, download INTEGER NOT NULL DEFAULT 0,
 started_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT '', observed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS connections_started ON connections(started_at);
