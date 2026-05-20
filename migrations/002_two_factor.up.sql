-- 2FA TOTP per Confluence Security & Communication.
-- two_factor_secret holds the AES-256-GCM ciphertext of the confirmed TOTP
-- secret (RFC 6238). two_factor_pending_secret stores the secret during the
-- setup wizard (between initiate and confirm) and is cleared once the user
-- proves possession by entering a valid code. two_factor_recovery_codes is
-- a JSON array of SHA-256 hex hashes (one-shot codes — never reversible).
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_secret TEXT;
ALTER TABLE users ADD COLUMN two_factor_pending_secret TEXT;
ALTER TABLE users ADD COLUMN two_factor_confirmed_at DATETIME;
ALTER TABLE users ADD COLUMN two_factor_recovery_codes TEXT;
