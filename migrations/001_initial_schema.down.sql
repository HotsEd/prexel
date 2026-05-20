-- Reverse of 001_initial_schema.up.sql.

DROP TRIGGER IF EXISTS trg_secrets_updated_at;
DROP TRIGGER IF EXISTS trg_domains_updated_at;
DROP TRIGGER IF EXISTS trg_servers_updated_at;
DROP TRIGGER IF EXISTS trg_apps_updated_at;

DROP INDEX IF EXISTS idx_deployments_app_created;
DROP INDEX IF EXISTS idx_refresh_tokens_user_id;
DROP INDEX IF EXISTS idx_refresh_tokens_family_id;

DROP TABLE IF EXISTS secrets;
DROP TABLE IF EXISTS deployments;
DROP TABLE IF EXISTS domains;
DROP TABLE IF EXISTS apps;
DROP TABLE IF EXISTS servers;
DROP TABLE IF EXISTS git_sources;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS settings;
