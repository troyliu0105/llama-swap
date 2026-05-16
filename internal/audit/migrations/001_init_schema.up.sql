CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    api_key    TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS request_log (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    metric_id          INTEGER NOT NULL DEFAULT -1,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    model              TEXT NOT NULL,
    req_path           TEXT NOT NULL DEFAULT '',
    status_code        INTEGER NOT NULL DEFAULT 0,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cached_tokens      INTEGER NOT NULL DEFAULT -1,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    tokens_per_second  REAL NOT NULL DEFAULT -1,
    prompt_per_second  REAL NOT NULL DEFAULT -1,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_rl_user_created ON request_log(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_rl_created ON request_log(created_at);
CREATE INDEX IF NOT EXISTS idx_rl_model ON request_log(model);
CREATE INDEX IF NOT EXISTS idx_rl_metric_id ON request_log(metric_id);

CREATE TABLE IF NOT EXISTS sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    model        TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    req_path     TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_fp ON sessions(user_id, fingerprint);

CREATE TABLE IF NOT EXISTS captures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    request_log_id  INTEGER NOT NULL REFERENCES request_log(id) ON DELETE CASCADE,
    data            BLOB NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_captures_session ON captures(session_id);
CREATE INDEX IF NOT EXISTS idx_captures_created ON captures(created_at);
