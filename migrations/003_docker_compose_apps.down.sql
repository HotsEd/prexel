-- Revert Docker Compose app fields. Existing docker_compose rows cannot be
-- represented in the old schema, so convert them to dockerfile placeholders.

PRAGMA foreign_keys=off;

CREATE TABLE apps_old (
    id                          TEXT PRIMARY KEY,
    name                        TEXT NOT NULL UNIQUE,
    description                 TEXT,
    server_id                   TEXT REFERENCES servers(id),
    git_source_id               TEXT REFERENCES git_sources(id),
    repo_url                    TEXT,
    branch                      TEXT NOT NULL DEFAULT 'main',
    git_commit_sha              TEXT,
    build_type                  TEXT NOT NULL DEFAULT 'dockerfile' CHECK (build_type IN ('dockerfile', 'docker_image')),
    dockerfile_path             TEXT NOT NULL DEFAULT 'Dockerfile',
    build_context               TEXT NOT NULL DEFAULT '.',
    dockerfile_inline           TEXT,
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

INSERT INTO apps_old (
    id, name, description, server_id, git_source_id, repo_url, branch, git_commit_sha,
    build_type, dockerfile_path, build_context, dockerfile_inline,
    image_name, image_tag, install_command, build_command, start_command,
    port, host_port, container_name,
    health_check_enabled, health_check_path, health_check_method, health_check_port,
    health_check_return_code, health_check_interval, health_check_timeout,
    health_check_retries, health_check_start_period,
    limits_memory, limits_cpus, env_vars, status,
    created_at, updated_at
)
SELECT
    id, name, description, server_id, git_source_id, repo_url, branch, git_commit_sha,
    CASE WHEN build_type = 'docker_compose' THEN 'dockerfile' ELSE build_type END,
    dockerfile_path, build_context, dockerfile_inline,
    image_name, image_tag, install_command, build_command, start_command,
    port, host_port, container_name,
    health_check_enabled, health_check_path, health_check_method, health_check_port,
    health_check_return_code, health_check_interval, health_check_timeout,
    health_check_retries, health_check_start_period,
    limits_memory, limits_cpus, env_vars, status,
    created_at, updated_at
FROM apps;

DROP TABLE apps;
ALTER TABLE apps_old RENAME TO apps;

PRAGMA foreign_keys=on;
