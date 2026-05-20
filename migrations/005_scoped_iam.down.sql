-- SQLite cannot drop columns safely without rebuilding tables; keep this
-- migration effectively irreversible for development instances.
DROP INDEX IF EXISTS idx_team_members_role_id;
DROP INDEX IF EXISTS idx_roles_scope;
