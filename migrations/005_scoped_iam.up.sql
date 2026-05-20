-- Scoped IAM: global roles remain on users.role_id; team roles live on memberships.

ALTER TABLE roles ADD COLUMN scope TEXT NOT NULL DEFAULT 'both' CHECK (scope IN ('global', 'team', 'both'));
ALTER TABLE team_members ADD COLUMN role_id TEXT REFERENCES roles(id);

UPDATE roles SET scope = 'global' WHERE slug = 'admin';
UPDATE roles SET scope = 'team' WHERE slug = 'developer';
UPDATE roles SET scope = 'both' WHERE slug = 'viewer';

UPDATE team_members
SET role_id = COALESCE(
    (SELECT role_id FROM users WHERE users.id = team_members.user_id),
    (SELECT id FROM roles WHERE slug = 'developer'),
    (SELECT id FROM roles WHERE slug = 'viewer')
)
WHERE role_id IS NULL;

CREATE INDEX idx_roles_scope ON roles(scope);
CREATE INDEX idx_team_members_role_id ON team_members(role_id);
