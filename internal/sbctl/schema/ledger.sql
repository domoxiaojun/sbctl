CREATE TABLE IF NOT EXISTS totals (user_id TEXT PRIMARY KEY, upload INTEGER NOT NULL, download INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cursors (
 user_id TEXT NOT NULL, direction TEXT NOT NULL, epoch TEXT NOT NULL,
 value INTEGER NOT NULL, seen INTEGER NOT NULL, PRIMARY KEY(user_id,direction,epoch)
);
CREATE TABLE IF NOT EXISTS series_totals (
 scope TEXT NOT NULL, key TEXT NOT NULL, protocol TEXT NOT NULL, port INTEGER NOT NULL,
 upload INTEGER NOT NULL, download INTEGER NOT NULL, started_at TEXT NOT NULL, active INTEGER NOT NULL,
 PRIMARY KEY(scope,key)
);
CREATE TABLE IF NOT EXISTS series_cursors (
 scope TEXT NOT NULL, key TEXT NOT NULL, direction TEXT NOT NULL, epoch TEXT NOT NULL,
 value INTEGER NOT NULL, seen INTEGER NOT NULL, PRIMARY KEY(scope,key,direction,epoch)
);
