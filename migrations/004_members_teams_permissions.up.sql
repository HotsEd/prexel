-- Members, roles/permissions and teams.
-- Roles are instance-scoped. Teams scope apps and membership.

CREATE TABLE roles (
    id          TEXT PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT,
    is_admin    INTEGER NOT NULL DEFAULT 0,
    is_system   INTEGER NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE users ADD COLUMN name TEXT;
ALTER TABLE users ADD COLUMN avatar_path TEXT;
ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'blocked'));
ALTER TABLE users ADD COLUMN role_id TEXT REFERENCES roles(id);

CREATE TABLE teams (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    description TEXT,
    color       TEXT NOT NULL DEFAULT '#10b981',
    avatar_path TEXT,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE team_members (
    team_id    TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (team_id, user_id)
);

ALTER TABLE apps ADD COLUMN team_id TEXT REFERENCES teams(id) ON DELETE SET NULL;

CREATE INDEX idx_users_role_id ON users(role_id);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_apps_team_id ON apps(team_id);
CREATE INDEX idx_team_members_user_id ON team_members(user_id);

CREATE TRIGGER trg_roles_updated_at
AFTER UPDATE ON roles FOR EACH ROW
BEGIN
    UPDATE roles SET updated_at = unixepoch() WHERE id = OLD.id;
END;

CREATE TRIGGER trg_teams_updated_at
AFTER UPDATE ON teams FOR EACH ROW
BEGIN
    UPDATE teams SET updated_at = unixepoch() WHERE id = OLD.id;
END;
