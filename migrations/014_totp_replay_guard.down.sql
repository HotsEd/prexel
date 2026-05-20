-- Down for 014. We drop the replay-guard table; there is no way to
-- "un-invalidate" the recovery codes that were nulled in up.sql, but
-- that's fine — users can regenerate from the 2FA settings page,
-- which is the same UX they hit in the upgrade direction.

DROP INDEX IF EXISTS totp_used_window;
DROP TABLE IF EXISTS totp_used;
