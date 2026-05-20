-- Roll back the schema. Backfill of per-row credentials into the
-- singleton (settings) is NOT attempted — the down path is only used
-- in dev and emergency rollback scenarios, and re-collapsing N apps
-- into 1 singleton is lossy by design.
DROP INDEX IF EXISTS git_sources_installation_id_unique;

ALTER TABLE git_sources DROP COLUMN account_type;
ALTER TABLE git_sources DROP COLUMN account_login;
ALTER TABLE git_sources DROP COLUMN app_private_key;
ALTER TABLE git_sources DROP COLUMN app_slug;
ALTER TABLE git_sources DROP COLUMN app_id;
