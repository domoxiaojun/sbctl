package sbctl

import (
	"context"
	"database/sql"
	"embed"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"time"
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
func connect(path string, create bool) (*sql.DB, *sql.Conn) {
	mode := "rw"
	if create {
		mode = "rwc"
	}
	return connectMode(path, mode)
}

func connectMode(path, mode string) (*sql.DB, *sql.Conn) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {mode}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}}
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	check(e)
	db.SetMaxOpenConns(1)
	conn, e := db.Conn(context.Background())
	if e != nil {
		db.Close()
		check(e)
	}
	return db, conn
}
func transaction(path string, write bool, fn func(*DB)) {
	db, conn := connect(path, false)
	defer db.Close()
	defer conn.Close()
	d := &DB{conn}
	if str(d.Value("SELECT value FROM meta WHERE key='schema_version'")) != schemaVersion {
		fail("SCHEMA_MISMATCH", "数据库版本不兼容", 503)
	}
	begin := "BEGIN"
	if write {
		begin = "BEGIN IMMEDIATE"
	} else {
		d.Exec("PRAGMA query_only=ON")
	}
	d.Exec(begin)
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	fn(d)
	d.Exec("COMMIT")
}
func initialize(path, kind string) {
	check(os.MkdirAll(filepath.Dir(path), 0755))
	unlock := fileLock(path+".migration.lock", 5*time.Second)
	defer unlock()
	db, conn := connect(path, true)
	defer db.Close()
	defer conn.Close()
	d := &DB{conn}
	if str(d.Value("PRAGMA journal_mode=DELETE")) != "delete" {
		fail("JOURNAL_MODE", "无法设置持久日志模式", 503)
	}
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
