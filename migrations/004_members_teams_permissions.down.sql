DROP TRIGGER IF EXISTS trg_teams_updated_at;
DROP TRIGGER IF EXISTS trg_roles_updated_at;

DROP INDEX IF EXISTS idx_team_members_user_id;
DROP INDEX IF EXISTS idx_apps_team_id;
DROP INDEX IF EXISTS idx_users_status;
DROP INDEX IF EXISTS idx_users_role_id;

ALTER TABLE apps DROP COLUMN team_id;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;

ALTER TABLE users DROP COLUMN role_id;
ALTER TABLE users DROP COLUMN status;
ALTER TABLE users DROP COLUMN avatar_path;
ALTER TABLE users DROP COLUMN name;

DROP TABLE IF EXISTS roles;
