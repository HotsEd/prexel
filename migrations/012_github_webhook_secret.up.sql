-- Migration 012 — GitHub webhook secret on git_sources.
--
-- The webhook secret is the value the operator pastes into the GitHub
-- App's webhook settings. We use it to verify the HMAC-SHA256 signature
-- (`X-Hub-Signature-256`) sent on every push delivery.
--
-- It is NOT credential-grade — knowing it doesn't grant access to
-- anything except the ability to forge webhook deliveries that this
-- Prexel instance would accept as authentic. Stored as plain TEXT for
-- that reason; the at-rest cipher reserved for App private keys, SSH
-- keys, and PATs would be overkill here.
--
-- Per-source rather than per-instance because each github_app row is
-- registered with its own GitHub App, which carries its own webhook
-- URL + secret. Sharing one secret across sources would mean any source
-- could be triggered by any source's webhook.

ALTER TABLE git_sources ADD COLUMN webhook_secret TEXT;
