CREATE TABLE IF NOT EXISTS codex_quota_snapshots (
    account_name TEXT PRIMARY KEY,
    snapshot TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
