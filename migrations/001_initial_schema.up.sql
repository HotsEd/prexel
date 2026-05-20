-- Initial schema for Prexel MVP v0.1.
-- Reference: Confluence Data Models (page 884738).
-- All `updated_at` columns are maintained by triggers defined at the bottom.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'admin',
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Refresh tokens use SHA-256 hashes; raw tokens are never stored.
-- family_id groups rotations: reuse of a revoked token from a family
-- invalidates the entire family (token-family rotation, Tech Review §9).
CREATE TABLE refresh_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id  TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    revoked_at INTEGER,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);
CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);

CREATE TABLE git_sources (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL CHECK (type IN ('github_app', 'ssh_key', 'token')),
    name            TEXT NOT NULL,
    installation_id TEXT,
    private_key     BLOB,      -- encrypted
    public_key      TEXT,
    token           BLOB,      -- encrypted
    created_at      INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE servers (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL UNIQUE,
    type                  TEXT NOT NULL CHECK (type IN ('local', 'remote')),
    host                  TEXT,
    port                  INTEGER NOT NULL DEFAULT 22,
    user                  TEXT,
    private_key           BLOB,    -- encrypted
    host_key_fingerprint  TEXT,
    status                TEXT NOT NULL DEFAULT 'unknown',
    docker_version        TEXT,
    last_checked_at       INTEGER,
    created_at            INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at            INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE apps (
    id                          TEXT PRIMARY KEY,
    name                        TEXT NOT NULL UNIQUE,
    description                 TEXT,
    server_id                   TEXT REFERENCES servers(id),
    git_source_id               TEXT REFERENCES git_sources(id),
    repo_url                    TEXT,
    branch                      TEXT NOT NULL DEFAULT 'main',
    git_commit_sha              TEXT,
    build_type                  TEXT NOT NULL DEFAULT 'dockerfile' CHECK (build_type IN ('dockerfile', 'docker_image', 'docker_compose')),
    dockerfile_path             TEXT NOT NULL DEFAULT 'Dockerfile',
    build_context               TEXT NOT NULL DEFAULT '.',
    dockerfile_inline           TEXT,
    compose_file                TEXT,
    compose_inline              TEXT,
    image_name                  TEXT,
    image_tag                   TEXT NOT NULL DEFAULT 'latest',
    install_command             TEXT,
    build_command               TEXT,
    start_command               TEXT,
    port                        INTEGER,
    host_port                   INTEGER,
    container_name              TEXT,
    health_check_enabled        INTEGER NOT NULL DEFAULT 1,
    health_check_path           TEXT NOT NULL DEFAULT '/',
    health_check_method         TEXT NOT NULL DEFAULT 'GET',
    health_check_port           INTEGER,
    health_check_return_code    INTEGER NOT NULL DEFAULT 200,
    health_check_interval       INTEGER NOT NULL DEFAULT 30,
    health_check_timeout        INTEGER NOT NULL DEFAULT 60,
    health_check_retries        INTEGER NOT NULL DEFAULT 3,
    health_check_start_period   INTEGER NOT NULL DEFAULT 10,
    limits_memory               TEXT,
    limits_cpus                 TEXT,
    env_vars                    TEXT NOT NULL DEFAULT '{}',
    status                      TEXT NOT NULL DEFAULT 'idle',
    created_at                  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at                  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE domains (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    app_id          TEXT REFERENCES apps(id) ON DELETE CASCADE,
    is_primary      INTEGER NOT NULL DEFAULT 0,
    ssl_status      TEXT NOT NULL DEFAULT 'pending',
    ssl_expires_at  INTEGER,
    dns_verified    INTEGER NOT NULL DEFAULT 0,
    dns_verified_at INTEGER,
    dns_last_check  INTEGER,
    dns_check_count INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at      INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE deployments (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    commit_sha  TEXT,
    commit_msg  TEXT,
    branch      TEXT,
    image_tag   TEXT,
    rollback_of TEXT REFERENCES deployments(id),
    status      TEXT NOT NULL DEFAULT 'pending',
    log_path    TEXT,
    started_at  INTEGER,
    finished_at INTEGER,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_deployments_app_created ON deployments(app_id, created_at DESC);

CREATE TABLE secrets (
    id            TEXT PRIMARY KEY,
    app_id        TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    value         BLOB NOT NULL,    -- encrypted
    is_build_time INTEGER NOT NULL DEFAULT 0,
    is_multiline  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (app_id, key)
);

-- updated_at maintenance triggers.
CREATE TRIGGER trg_apps_updated_at
AFTER UPDATE ON apps FOR EACH ROW
BEGIN
    UPDATE apps SET updated_at = unixepoch() WHERE id = OLD.id;
END;

CREATE TRIGGER trg_servers_updated_at
AFTER UPDATE ON servers FOR EACH ROW
BEGIN
    UPDATE servers SET updated_at = unixepoch() WHERE id = OLD.id;
END;

CREATE TRIGGER trg_domains_updated_at
AFTER UPDATE ON domains FOR EACH ROW
BEGIN
    UPDATE domains SET updated_at = unixepoch() WHERE id = OLD.id;
END;

CREATE TRIGGER trg_secrets_updated_at
AFTER UPDATE ON secrets FOR EACH ROW
BEGIN
    UPDATE secrets SET updated_at = unixepoch() WHERE id = OLD.id;
END;
