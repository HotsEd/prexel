-- Migration 011 — App runtime extensions.
--
-- Bundle of MVP-completing knobs that operators expect on any
-- "production" PaaS but that we punted on in the initial schema:
--
--   * Pre/post-deploy hook commands (Rails/Django migrations etc.)
--   * Restart policy at container level
--   * Auto-deploy on push (drives the github webhook handler)
--   * Advanced resource limits (memory_swap, swappiness, reservation,
--     cpuset, cpu_shares) — exposed under an "Avançado" collapsible
--   * Build args toggles (inject ARGs / SOURCE_COMMIT cache control)
--   * Custom Docker labels (JSON map applied at container create)
--   * Persistent storage (`app_volumes` — named volumes or host bind
--     mounts) — without this no stateful app is viable
--   * Tags (`app_tags` — many-to-one, used for filtering on the
--     dashboard)
--   * `force_https` toggle per domain (default ON; off only when an
--     operator explicitly serves plain HTTP)
--
-- Every new column is nullable / has a safe default so older rows
-- continue to deploy without modification.

ALTER TABLE apps ADD COLUMN pre_deploy_command       TEXT;
ALTER TABLE apps ADD COLUMN post_deploy_command      TEXT;

-- Docker restart policy. Values match docker's CLI:
--   no          — never restart (default for one-shot containers)
--   always      — restart whenever it exits
--   on-failure  — restart only on non-zero exit
--   unless-stopped — restart unless the operator stopped it manually
-- We default to unless-stopped because that's what "long-running
-- service" means; existing rows pick it up via default.
ALTER TABLE apps ADD COLUMN restart_policy           TEXT NOT NULL DEFAULT 'unless-stopped'
    CHECK (restart_policy IN ('no', 'always', 'on-failure', 'unless-stopped'));

-- Auto-deploy: when non-empty, pushes to this branch on the configured
-- git source trigger a deploy via the GitHub webhook endpoint. We
-- store the branch (not just a bool) so the operator can deploy off
-- a different branch than the one tracked for auto-deploy without
-- losing the setting.
ALTER TABLE apps ADD COLUMN auto_deploy_branch       TEXT;

-- Build-time knobs. inject = whether `docker build --build-arg` is
-- populated from secrets marked `is_build_time`. source_commit_arg =
-- whether SOURCE_COMMIT is injected (changing this busts the build
-- cache, so it's opt-in).
ALTER TABLE apps ADD COLUMN build_args_inject        INTEGER NOT NULL DEFAULT 1;
ALTER TABLE apps ADD COLUMN build_args_source_commit INTEGER NOT NULL DEFAULT 0;

-- Advanced resource limits. All strings/integers because docker SDK
-- accepts them as such; validation lives in the service layer.
ALTER TABLE apps ADD COLUMN limits_memory_swap        TEXT;
ALTER TABLE apps ADD COLUMN limits_memory_swappiness  INTEGER;
ALTER TABLE apps ADD COLUMN limits_memory_reservation TEXT;
ALTER TABLE apps ADD COLUMN limits_cpuset             TEXT;
ALTER TABLE apps ADD COLUMN limits_cpu_shares         INTEGER;

-- Custom Docker labels. Stored as JSON map<string,string>, merged
-- with prexel.* labels at container create time. Prexel labels win
-- on conflict.
ALTER TABLE apps ADD COLUMN docker_labels             TEXT NOT NULL DEFAULT '{}';

-- ── Domains: per-domain HTTPS enforcement toggle ───────────────────
ALTER TABLE domains ADD COLUMN force_https INTEGER NOT NULL DEFAULT 1;

-- ── Persistent storage ────────────────────────────────────────────
-- Named volumes (Docker manages the path) or bind mounts (operator
-- specifies the host path). For Compose apps the volumes from the
-- compose file take precedence; entries here apply to non-compose
-- apps and to "extra" volumes added on top of a service.
--
-- service column: NULL for single-container apps. For compose apps,
-- which service the volume attaches to (matches `domains.service`
-- semantics from migration 010).
CREATE TABLE app_volumes (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service     TEXT,
    mount_path  TEXT NOT NULL,
    host_path   TEXT,                 -- NULL = named volume
    is_named    INTEGER NOT NULL DEFAULT 1,
    read_only   INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    -- A given (service, mount_path) pair must be unique per app —
    -- otherwise the operator could declare two mounts colliding on
    -- the same in-container path.
    UNIQUE (app_id, service, mount_path)
);
CREATE INDEX idx_app_volumes_app ON app_volumes(app_id);

CREATE TRIGGER trg_app_volumes_updated_at
AFTER UPDATE ON app_volumes FOR EACH ROW
BEGIN
    UPDATE app_volumes SET updated_at = unixepoch() WHERE id = OLD.id;
END;

-- ── Tags ──────────────────────────────────────────────────────────
-- Lightweight string labels. Many-to-many via the join table, but
-- the dictionary is per-instance so dashboards can list "all
-- distinct tags". Names are normalised to lowercase + kebab on the
-- service layer; the unique constraint is on the raw stored value.
CREATE TABLE tags (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    color      TEXT,                  -- optional hex like "#3b82f6"
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE app_tags (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (app_id, tag_id)
);
CREATE INDEX idx_app_tags_tag ON app_tags(tag_id);
