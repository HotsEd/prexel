ALTER TABLE users DROP COLUMN two_factor_enabled;
ALTER TABLE users DROP COLUMN two_factor_secret;
ALTER TABLE users DROP COLUMN two_factor_pending_secret;
ALTER TABLE users DROP COLUMN two_factor_confirmed_at;
ALTER TABLE users DROP COLUMN two_factor_recovery_codes;
