-- git_sources gains per-row GitHub App credentials.
--
-- Until now the GitHub App registration (app_id, slug, private_key)
-- lived as a singleton in the `settings` table. That meant exactly ONE
-- GitHub App per Prexel instance — operators who participate in
-- multiple GitHub accounts (personal + an org) had no way to wire both
-- without one overwriting the other and silently breaking JWTs for the
-- previous installation_id.
--
-- New model: each git_sources row carries its own App credentials and
-- the account it was installed on. Multiple GitHub Apps coexist; the
-- runtime no longer keeps any global App state.
--
-- Schema-only: backfilling the existing singleton into the new columns
-- requires base64-decoding + cipher migration, which is impossible in
-- pure SQLite. The Go runtime runs `gitsrc.BackfillSingletonAppConfig`
-- right after `db.Migrate` to copy the legacy `settings` values into
-- the new per-row columns, then deletes the singleton keys. See
-- internal/gitsrc/migrate.go for the implementation.

ALTER TABLE git_sources ADD COLUMN app_id          TEXT;
ALTER TABLE git_sources ADD COLUMN app_slug        TEXT;
ALTER TABLE git_sources ADD COLUMN app_private_key BLOB;  -- encrypted
ALTER TABLE git_sources ADD COLUMN account_login   TEXT;  -- "octocat" / "acme-corp"
ALTER TABLE git_sources ADD COLUMN account_type    TEXT;  -- "User" | "Organization"

-- Unique installation_id (partial — null is allowed for rows that
-- finished the manifest flow but haven't been installed yet). SQLite
-- treats multiple NULLs as distinct under UNIQUE, so this never
-- blocks legitimate "App created, install pending" rows.
CREATE UNIQUE INDEX git_sources_installation_id_unique
    ON git_sources(installation_id)
    WHERE installation_id IS NOT NULL;
