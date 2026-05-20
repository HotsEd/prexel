-- Instance-wide settings, typed.
--
-- Until now the `settings` table (key TEXT, value TEXT) held everything ad-hoc:
-- instance_id, instance_url, tls_mode, setup_completed. That worked for boot
-- bookkeeping but it forced every read to parse strings and every write to
-- guess at a schema. As we grew real configuration knobs (resource defaults,
-- cleanup cadence, concurrent deploy caps, maintenance mode) the key-value
-- table became a liability — no constraints, no defaults, no atomic update.
--
-- This migration introduces a singleton `instance_settings` row (id = 1)
-- carrying the typed fields. We backfill from `settings` so the existing
-- instance URL / tls mode don't get lost. `settings` stays alive for the
-- pure-bookkeeping keys (instance_id, setup_completed) — those are not
-- "settings the operator edits" and they have no need to be in a wide table.

CREATE TABLE instance_settings (
    id                       INTEGER PRIMARY KEY CHECK (id = 1),

    -- Domain & TLS — editable post-setup. tls_mode is enforced by check; the
    -- application layer additionally refuses `letsencrypt` when instance_url
    -- is empty (Let's Encrypt needs a real hostname).
    instance_url             TEXT NOT NULL DEFAULT '',
    tls_mode                 TEXT NOT NULL DEFAULT 'self-signed'
                             CHECK (tls_mode IN ('self-signed', 'letsencrypt')),

    -- Default resource limits applied to apps created without explicit values.
    -- NULL/empty means "no default" (the Docker daemon decides).
    default_memory_limit     TEXT,
    default_cpu_limit        TEXT,

    -- Auto cleanup. The loop runs in-process and prunes when disk_threshold
    -- is exceeded; image_retention caps the number of historic images per
    -- app regardless of disk pressure.
    cleanup_enabled          INTEGER NOT NULL DEFAULT 0,
    cleanup_schedule         TEXT NOT NULL DEFAULT '0 3 * * *',
    cleanup_disk_threshold   INTEGER NOT NULL DEFAULT 80
                             CHECK (cleanup_disk_threshold BETWEEN 0 AND 100),
    cleanup_image_retention  INTEGER NOT NULL DEFAULT 5
                             CHECK (cleanup_image_retention >= 1),

    -- Global semaphore for the deploy engine. Per-app locking already exists;
    -- this caps concurrent deploys across the whole instance so a parallel
    -- "deploy everything" doesn't saturate the build host.
    max_concurrent_deploys   INTEGER NOT NULL DEFAULT 3
                             CHECK (max_concurrent_deploys >= 1),

    -- Maintenance mode. When enabled, the API replies 503 to every request
    -- except healthz, /auth/*, and PATCH /instance/settings (so the admin can
    -- disable it back). UI shows a banner.
    maintenance_mode         INTEGER NOT NULL DEFAULT 0,
    maintenance_message      TEXT    NOT NULL
                             DEFAULT 'Prexel is currently under maintenance.',

    updated_at               INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Singleton row. PK is the literal 1; trying to insert a second row fails
-- on the CHECK. Application code asserts this in tests.
INSERT INTO instance_settings (id) VALUES (1);

-- Backfill from the legacy key-value `settings` table so we don't lose the
-- domain the user wired up during setup.
UPDATE instance_settings SET
    instance_url = COALESCE(
        (SELECT value FROM settings WHERE key = 'instance_url'),
        ''
    ),
    tls_mode = COALESCE(
        (SELECT value FROM settings WHERE key = 'tls_mode'),
        'self-signed'
    )
WHERE id = 1;

CREATE TRIGGER trg_instance_settings_updated_at
AFTER UPDATE ON instance_settings FOR EACH ROW
BEGIN
    UPDATE instance_settings SET updated_at = unixepoch() WHERE id = 1;
END;
