-- Down: drop webhook_secret added in 012_github_webhook_secret.up.sql.

ALTER TABLE git_sources DROP COLUMN webhook_secret;
