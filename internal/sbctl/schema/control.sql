CREATE TABLE IF NOT EXISTS admins (
 id TEXT PRIMARY KEY, login_name TEXT UNIQUE NOT NULL, display_name TEXT NOT NULL,
 password_hash TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY, admin_id TEXT NOT NULL REFERENCES admins(id),
 expires_at REAL NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS login_failures (subject TEXT NOT NULL, attempted_at REAL NOT NULL);
CREATE INDEX IF NOT EXISTS login_failure_time ON login_failures(subject,attempted_at);
CREATE TABLE IF NOT EXISTS nodes (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, region TEXT NOT NULL, location TEXT NOT NULL,
 management_address TEXT NOT NULL, public_host TEXT NOT NULL,
 connection_json TEXT NOT NULL, endpoints_json TEXT NOT NULL,
 template_hash TEXT NOT NULL, image_digest TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0,
 applied_revision INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS groups (id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS group_nodes (
 group_id TEXT NOT NULL REFERENCES groups(id), node_id TEXT NOT NULL REFERENCES nodes(id),
 PRIMARY KEY(group_id,node_id)
);
CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, display_name TEXT NOT NULL, note TEXT NOT NULL, status TEXT NOT NULL,
 revision INTEGER NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS grants (
 user_id TEXT NOT NULL REFERENCES users(id), group_id TEXT NOT NULL REFERENCES groups(id),
 PRIMARY KEY(user_id,group_id)
);
CREATE TABLE IF NOT EXISTS credentials (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), node_id TEXT NOT NULL REFERENCES nodes(id),
 protocol TEXT NOT NULL, generation INTEGER NOT NULL, secret_ciphertext TEXT NOT NULL,
 fingerprint TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, verified_revision INTEGER,
 UNIQUE(user_id,node_id,protocol,generation), UNIQUE(node_id,protocol,fingerprint)
);
CREATE TABLE IF NOT EXISTS revocations (
 node_id TEXT NOT NULL REFERENCES nodes(id), fingerprint TEXT NOT NULL, credential_id TEXT NOT NULL,
 created_at TEXT NOT NULL, PRIMARY KEY(node_id,fingerprint)
);
CREATE TABLE IF NOT EXISTS subscriptions (
 user_id TEXT PRIMARY KEY REFERENCES users(id), token_hash TEXT UNIQUE NOT NULL, token_ciphertext TEXT NOT NULL,
 generation INTEGER NOT NULL, state TEXT NOT NULL, publish_revision INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS operations (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, user_id TEXT REFERENCES users(id), subject TEXT NOT NULL,
 state TEXT NOT NULL, priority INTEGER NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS operation_targets (
 operation_id TEXT NOT NULL REFERENCES operations(id), node_id TEXT NOT NULL REFERENCES nodes(id),
 state TEXT NOT NULL, message TEXT NOT NULL, request_id TEXT,
 PRIMARY KEY(operation_id,node_id)
);
CREATE TABLE IF NOT EXISTS node_requests (
 id TEXT PRIMARY KEY, node_id TEXT NOT NULL REFERENCES nodes(id), revision INTEGER NOT NULL,
 payload_hash TEXT NOT NULL, payload_ciphertext TEXT NOT NULL, created_at TEXT NOT NULL,
 receipt_json TEXT, UNIQUE(node_id,revision)
);
CREATE TABLE IF NOT EXISTS outbox (
 id TEXT PRIMARY KEY, operation_id TEXT NOT NULL REFERENCES operations(id), node_id TEXT NOT NULL REFERENCES nodes(id),
 priority INTEGER NOT NULL, state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at REAL NOT NULL, lease_owner TEXT, lease_until REAL NOT NULL DEFAULT 0,
 claim_generation INTEGER NOT NULL DEFAULT 0, request_id TEXT
);
CREATE INDEX IF NOT EXISTS outbox_ready ON outbox(state,priority,next_attempt_at,lease_until);
CREATE TABLE IF NOT EXISTS idempotency (
 admin_id TEXT NOT NULL, key TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL,
 payload_hash TEXT NOT NULL, response_json TEXT NOT NULL, response_status INTEGER NOT NULL,
 created_at TEXT NOT NULL, PRIMARY KEY(admin_id,key)
);
CREATE TABLE IF NOT EXISTS alerts (
 id TEXT PRIMARY KEY, dedupe_key TEXT UNIQUE NOT NULL, title TEXT NOT NULL, description TEXT NOT NULL,
 severity TEXT NOT NULL, state TEXT NOT NULL, node_id TEXT, created_at TEXT NOT NULL, ack_by TEXT
);
CREATE TABLE IF NOT EXISTS audit (
 id INTEGER PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, target_id TEXT NOT NULL,
 summary TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS node_management (
 node_id TEXT PRIMARY KEY REFERENCES nodes(id), version INTEGER NOT NULL DEFAULT 0,
 binding_json TEXT NOT NULL DEFAULT '{}', checked_at TEXT, check_json TEXT
);
CREATE TABLE IF NOT EXISTS node_jobs (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, node_id TEXT NOT NULL, state TEXT NOT NULL,
 input_json TEXT NOT NULL, result_json TEXT, error_code TEXT, message TEXT,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 lease_owner TEXT, lease_until REAL NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS user_management (
 user_id TEXT PRIMARY KEY REFERENCES users(id), version INTEGER NOT NULL DEFAULT 0, archived_at TEXT
);
CREATE TABLE IF NOT EXISTS node_catalog (
 node_id TEXT PRIMARY KEY REFERENCES nodes(id), catalog_json TEXT NOT NULL, checked_at TEXT NOT NULL
);
