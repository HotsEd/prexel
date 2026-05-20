-- Domains can now target a specific service within a Compose app.
--
-- Pre-010: every app was treated as single-container — a domain was
-- bound to an app and Caddy proxied to `prexel-<app>:<app.port>`.
-- Compose apps have N services with N (different) ports; the
-- operator needs to say *which* service / which port a given domain
-- routes to.
--
-- New columns are nullable for backwards compat: single-container
-- apps leave them NULL and the routing layer falls back to the
-- existing app-level port. When set, the routing layer composes
-- `prexel-<app>-<service>:<port>` for Caddy upstream.

ALTER TABLE domains ADD COLUMN service TEXT;     -- e.g. "web", "api"
ALTER TABLE domains ADD COLUMN port    INTEGER;  -- container port

-- A given (app, service, port) tuple can only have ONE domain
-- routing to it. Without this, two domains pointing at the same
-- service+port would race in the Caddy config. Partial UNIQUE so
-- the constraint only applies when service is actually set.
CREATE UNIQUE INDEX domains_service_route_unique
    ON domains(app_id, service, port)
    WHERE service IS NOT NULL;
