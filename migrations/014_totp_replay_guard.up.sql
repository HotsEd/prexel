-- Migration 014 — TOTP replay guard + recovery-code hash rotation.
--
-- Part 1: TOTP replay window
-- --------------------------
-- TOTP codes are valid for an entire 30-second window plus the ±1 step
-- skew the library tolerates. Without a replay guard, an attacker who
-- shoulder-surfs / phishes a single code can reuse it for up to 90s
-- (and the legitimate user wouldn't notice — their code worked the
-- first time). The OWASP MFA cheat sheet calls this out specifically.
--
-- The fix is RFC 6238 §5.2: once a TOTP is accepted, refuse it again
-- inside the same window. We store (user_id, code, window_ts) and rely
-- on the primary key to fail the second INSERT. window_ts is the start
-- of the 30s window (floor(unix_seconds / 30) * 30) so distinct windows
-- get distinct keys naturally.
--
-- Cleanup happens inline at insert time (DELETE … WHERE window_ts <
-- now - 90s). 90s = current ± skew, beyond which a row can't possibly
-- still cause a collision. No background worker, no race.

CREATE TABLE totp_used (
    user_id   TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code      TEXT    NOT NULL,
    window_ts INTEGER NOT NULL,
    PRIMARY KEY (user_id, code, window_ts)
);

-- Hot-path index for the cleanup DELETE.
CREATE INDEX totp_used_window ON totp_used(window_ts);

-- Part 2: Recovery codes → bcrypt
-- --------------------------------
-- Recovery codes used to be stored as a JSON array of SHA-256 hex
-- digests inside users.two_factor_recovery_codes. SHA-256 is fast,
-- which is exactly the problem: a leaked DB lets an attacker GPU-grind
-- the 50-bit codes in seconds. We're moving to bcrypt (cost matched
-- to the password column) so the same DB leak costs ~CPU-hours per
-- code instead.
--
-- The schema doesn't change — the column already holds a JSON string.
-- What changes is the content: hashes are bcrypt strings ($2a$…)
-- rather than 64-char hex. To avoid leaving any user logged in with
-- the old-format codes (mixed-format JSON would be a footgun), we
-- INVALIDATE every existing recovery code list as part of this
-- migration. Affected users still have TOTP — they just can't fall
-- back to a recovery code until they hit /auth/2fa/recovery-codes
-- and regenerate. The UI is expected to nag them on the settings
-- page after upgrade.
--
-- This is the safer choice: silently honouring SHA-256 strings would
-- mean a leaked pre-migration DB still grants 2FA bypass forever.

UPDATE users
   SET two_factor_recovery_codes = NULL
 WHERE two_factor_recovery_codes IS NOT NULL;
