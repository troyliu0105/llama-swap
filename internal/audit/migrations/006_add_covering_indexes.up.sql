CREATE INDEX IF NOT EXISTS idx_rl_codex_covering
    ON request_log(codex_account, created_at, input_tokens, cached_tokens);

CREATE INDEX IF NOT EXISTS idx_rl_user_period_covering
    ON request_log(user_id, created_at, codex_account, model, input_tokens, output_tokens, cached_tokens);
