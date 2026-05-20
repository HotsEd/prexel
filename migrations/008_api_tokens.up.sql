-- Personal API tokens (PATs) — machine-friendly auth.
--
-- Up to now the only way to authenticate against /api/v1/* was a JWT
-- minted by /auth/login, which means the operator either has to keep a
-- session cookie alive or paste an access token that auto-expires in
-- 15 minutes. Neither plays nice with CI/CD, cron, or any script that
-- needs to call `prexel deploy` without a human in the loop.
--
-- This migration adds a long-lived, revocable credential per user. A
-- token authenticates AS the user — there is no separate scope matrix,
-- so it inherits whatever RBAC permissions the owning user has at the
-- moment the request comes in. (Revoking the user revokes the tokens
-- via ON DELETE CASCADE.)
--
-- Storage notes:
--   - We never store the raw token. `token_hash` is SHA-256 hex of the
--     full `prx_pat_<…>` string. SHA-256 is fine here (not bcrypt):
--     tokens are 256-bit machine-generated secrets, not user passwords,
--     and SHA-256 is the industry norm for this exact use case (GitHub,
--     GitLab, Linear, etc. all do this).
--   - `last_four` is the last 4 chars of the raw token, shown in the
--     listing so the operator can match "Which token is this?" against
--     whatever they stashed in their CI vault. 4 chars give 16^4 ≈ 65k
--     possibilities — high enough to disambiguate, low enough to be
--     useless to an attacker.
--   - `expires_at` and `last_used_at` are nullable. NULL expires_at
--     means "never" (operator choice on create). NULL last_used_at
--     means the token has never authenticated a request yet.
--   - `revoked_at` is a soft-delete tombstone so we keep auditability;
--     hard-deletes would erase "who created this rogue token?".

CREATE TABLE api_tokens (
    id            TEXT NOT NULL PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL UNIQUE,
    last_four     TEXT NOT NULL,
    expires_at    INTEGER,
    last_used_at  INTEGER,
    revoked_at    INTEGER,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Per-user listing.
CREATE INDEX api_tokens_user ON api_tokens(user_id);
-- Hot path: the auth middleware does `SELECT … WHERE token_hash = ?`
-- on every PAT request. The UNIQUE constraint on token_hash already
-- gives us an implicit index, but make it explicit for clarity.
CREATE INDEX api_tokens_hash ON api_tokens(token_hash);

-- Mirror the rest of the schema's convention of bumping updated_at on
-- writes. Mostly cosmetic on this table (rename is the only update
-- path we expose today) but keeps the audit story consistent.
CREATE TRIGGER api_tokens_updated_at
AFTER UPDATE ON api_tokens
BEGIN
    UPDATE api_tokens SET updated_at = strftime('%s', 'now') WHERE id = NEW.id;
END;
