package sbctl

import (
	"context"
	"database/sql"
	"embed"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema/*.sql
var schemas embed.FS

const schemaVersion = "3"

type DB struct{ conn *sql.Conn }

func (d *DB) Exec(query string, args ...any) int64 {
	r, e := d.conn.ExecContext(context.Background(), query, args...)
	check(e)
	n, e := r.RowsAffected()
	check(e)
	return n
}
func (d *DB) Rows(query string, args ...any) []M {
	rows, e := d.conn.QueryContext(context.Background(), query, args...)
	check(e)
	defer rows.Close()
	cols, e := rows.Columns()
	check(e)
	out := []M{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		check(rows.Scan(ptrs...))
		m := M{}
		for i, k := range cols {
			m[k] = vals[i]
		}
		out = append(out, m)
	}
	check(rows.Err())
	return out
}
func (d *DB) One(query string, args ...any) M {
	r := d.Rows(query, args...)
	if len(r) == 0 {
		return nil
	}
	return r[0]
}
func (d *DB) Value(query string, args ...any) any {
	row := d.One(query, args...)
	for _, v := range row {
		return v
	}
	return nil
}

type pooledHandle struct {
	path     string
	inode    uint64
	verified bool
	lastUsed time.Time
	db       *sql.DB
}

const maxPooledDatabases = 16

var dbPool = struct {
	sync.Mutex
	entries map[string]*pooledHandle
}{entries: map[string]*pooledHandle{}}

func openRawDB(path, mode string, maxConns int) *sql.DB {
	u := url.URL{Scheme: "file", Path: path}
	pragmas := []string{"foreign_keys(1)", "busy_timeout(5000)"}
	if mode != "ro" {
		pragmas = append(pragmas, "synchronous(FULL)", "wal_autocheckpoint(1)")
	}
	q := url.Values{"mode": {mode}, "_pragma": pragmas}
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	check(e)
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	return db
}

func pooledDB(path string, write bool) (*pooledHandle, *sql.Conn) {
	info, err := os.Stat(path)
	if err != nil {
		check(err)
	}
	ino := inode(info)
	nowTime := time.Now()
	key := path
	mode := "rw"
	if !write {
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err != nil {
			mode = "ro"
			key = path + "#ro"
		} else {
			_ = f.Close()
		}
	}

	dbPool.Lock()
	h, ok := dbPool.entries[key]
	if ok && h.inode != ino {
		_ = h.db.Close()
		delete(dbPool.entries, key)
		ok = false
	}
	if !ok {
		for len(dbPool.entries) >= maxPooledDatabases {
			var oldestKey string
			var oldestTime time.Time
			for k, cand := range dbPool.entries {
				if oldestKey == "" || cand.lastUsed.Before(oldestTime) {
					oldestKey, oldestTime = k, cand.lastUsed
				}
			}
			if oldestKey == "" {
				break
			}
			evicted := dbPool.entries[oldestKey]
			delete(dbPool.entries, oldestKey)
			_ = evicted.db.Close()
		}
		h = &pooledHandle{
			path:     path,
			inode:    ino,
			lastUsed: nowTime,
			db:       openRawDB(path, mode, 4),
		}
		dbPool.entries[key] = h
	} else {
		h.lastUsed = nowTime
	}
	db := h.db
	dbPool.Unlock()

	conn, e := db.Conn(context.Background())
	check(e)
	return h, conn
}

func invalidatePooledDB(path string) {
	dbPool.Lock()
	for _, k := range []string{path, path + "#ro"} {
		if h, ok := dbPool.entries[k]; ok {
			delete(dbPool.entries, k)
			_ = h.db.Close()
		}
	}
	dbPool.Unlock()
}

func connectMode(path, mode string) (*sql.DB, *sql.Conn) {
	db := openRawDB(path, mode, 1)
	conn, e := db.Conn(context.Background())
	if e != nil {
		db.Close()
		check(e)
	}
	return db, conn
}

func transaction(path string, write bool, fn func(*DB)) {
	h, conn := pooledDB(path, write)
	defer conn.Close()
	d := &DB{conn}

	dbPool.Lock()
	verified := h.verified
	dbPool.Unlock()
	if !verified {
		if str(d.Value("SELECT value FROM meta WHERE key='schema_version'")) != schemaVersion {
			conn.Close()
			invalidatePooledDB(path)
			fail("SCHEMA_MISMATCH", "数据库版本不兼容", 503)
		}
		dbPool.Lock()
		h.verified = true
		dbPool.Unlock()
	}

	if write {
		d.Exec("PRAGMA query_only=OFF")
		d.Exec("BEGIN IMMEDIATE")
	} else {
		d.Exec("PRAGMA query_only=ON")
		d.Exec("BEGIN")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
		if !write {
			_, _ = conn.ExecContext(context.Background(), "PRAGMA query_only=OFF")
		}
	}()
	fn(d)
	d.Exec("COMMIT")
	committed = true
}

func initialize(path, kind string) {
	check(os.MkdirAll(filepath.Dir(path), 0755))
	unlock := fileLock(path+".migration.lock", 5*time.Second)
	defer unlock()
	invalidatePooledDB(path)
	db, conn := connectMode(path, "rwc")
	defer db.Close()
	defer conn.Close()
	d := &DB{conn}
	modeStr := str(d.Value("PRAGMA journal_mode=WAL"))
	if modeStr != "wal" && modeStr != "memory" {
		fail("JOURNAL_MODE", "无法设置 WAL 日志模式", 503)
	}
	d.Exec("PRAGMA wal_autocheckpoint=1")
	d.Exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY,value TEXT NOT NULL)")
	existing := d.Value("SELECT value FROM meta WHERE key='schema_version'")
	if existing != nil && str(existing) != schemaVersion && str(existing) != "2" {
		fail("SCHEMA_MISMATCH", "旧数据版本不兼容，请使用新的数据目录", 503)
	}
	schema, e := schemas.ReadFile("schema/" + kind + ".sql")
	check(e)
	d.Exec("BEGIN IMMEDIATE")
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if kind == "control" && str(existing) == "2" {
		d.Exec("ALTER TABLE credentials RENAME TO credentials_v2")
	}
	d.Exec(string(schema))
	if kind == "control" && str(existing) == "2" {
		migrateCredentials(d)
	}
	if kind == "ledger" && str(existing) == "2" {
		d.Exec("ALTER TABLE series_totals ADD COLUMN tag TEXT NOT NULL DEFAULT ''")
		d.Exec("ALTER TABLE series_totals ADD COLUMN history_tag TEXT NOT NULL DEFAULT ''")
		for _, row := range d.Rows("SELECT key,protocol FROM series_totals WHERE scope='inbound'") {
			tag := str(row["key"])
			id := legacyInboundID(Endpoint{Protocol: str(row["protocol"]), Tag: tag})
			d.Exec("UPDATE series_cursors SET key=? WHERE scope='inbound' AND key=?", id, tag)
			d.Exec("UPDATE series_totals SET key=?,tag=?,history_tag=? WHERE scope='inbound' AND key=?", id, tag, tag, tag)
		}
	}
	d.Exec("INSERT OR IGNORE INTO meta VALUES ('schema_version',?)", schemaVersion)
	if existing != nil && str(existing) == "2" {
		d.Exec("UPDATE meta SET value=? WHERE key='schema_version'", schemaVersion)
	}
	d.Exec("COMMIT")
	_, _ = conn.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	mode := os.FileMode(0600)
	if kind == "traffic" {
		mode = 0640
	}
	check(os.Chmod(path, mode))
}

func migrateCredentials(d *DB) {
	for _, node := range d.Rows("SELECT id,endpoints_json FROM nodes") {
		endpoints := decodeEndpoints(node["endpoints_json"])
		byProtocol := map[string]string{}
		for _, endpoint := range endpoints {
			if byProtocol[endpoint.Protocol] != "" {
				fail("SCHEMA_MISMATCH", "旧版凭据无法唯一匹配入口", 503)
			}
			byProtocol[endpoint.Protocol] = endpoint.InboundID
		}
		d.Exec("UPDATE nodes SET endpoints_json=? WHERE id=?", string(canonical(endpoints)), node["id"])
		for _, row := range d.Rows("SELECT DISTINCT protocol FROM credentials_v2 WHERE node_id=?", node["id"]) {
			inboundID := byProtocol[str(row["protocol"])]
			if inboundID == "" {
				fail("SCHEMA_MISMATCH", "旧版凭据缺少入口绑定", 503)
			}
			d.Exec("INSERT INTO credentials SELECT id,user_id,node_id,?,protocol,generation,secret_ciphertext,fingerprint,revoked,verified_revision FROM credentials_v2 WHERE node_id=? AND protocol=?", inboundID, node["id"], row["protocol"])
		}
	}
	d.Exec("DROP TABLE credentials_v2")
}
func putMeta(d *DB, k, v string) {
	d.Exec("INSERT INTO meta VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v)
}
func meta(d *DB) M {
	m := M{}
	for _, r := range d.Rows("SELECT key,value FROM meta") {
		m[str(r["key"])] = r["value"]
	}
	return m
}
