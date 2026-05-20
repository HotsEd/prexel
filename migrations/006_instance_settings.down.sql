-- Restore the previous behaviour: instance_url / tls_mode go back into the
-- key-value `settings` table. Lossy by design — extra fields (cleanup, etc.)
-- are dropped.

INSERT OR REPLACE INTO settings (key, value, updated_at)
SELECT 'instance_url', instance_url, unixepoch()
FROM instance_settings WHERE id = 1 AND instance_url <> '';

INSERT OR REPLACE INTO settings (key, value, updated_at)
SELECT 'tls_mode', tls_mode, unixepoch()
FROM instance_settings WHERE id = 1;

DROP TRIGGER IF EXISTS trg_instance_settings_updated_at;
DROP TABLE IF EXISTS instance_settings;
