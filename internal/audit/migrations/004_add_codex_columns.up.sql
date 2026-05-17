ALTER TABLE request_log ADD COLUMN codex_account TEXT;
CREATE INDEX IF NOT EXISTS idx_rl_codex_account ON request_log(codex_account);
