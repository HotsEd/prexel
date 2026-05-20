-- DNS zones — the "apex" the operator owns (e.g. `meudominio.com`).
--
-- Until now `domains` was a flat list of FQDNs. That worked for the first
-- couple of apps, but the operator quickly loses visibility of WHICH apex
-- they own and which subdomains under it are wired into Prexel. It also
-- means we can't represent "I configured `*.meudominio.com` in my DNS
-- provider, please reuse that for every future subdomain" — we end up
-- DNS-checking every new domain individually even though one wildcard
-- probe could cover the whole zone.
--
-- This migration introduces:
--
--   dns_zones       — one row per apex the user has registered with us.
--                     Carries the verification state for the apex A record
--                     AND for an optional wildcard (`*.apex`).
--   domains.zone_id — FK back to the zone the FQDN belongs to (e.g.
--                     `api.meudominio.com` → zone `meudominio.com`).
--   domains.covered_by_wildcard — denormalized hint for the UI: "this
--                     domain's individual A check still happens, but the
--                     parent zone has a verified wildcard, so even if the
--                     individual check ever flakes you're covered."
--
-- Zones are auto-created when the user adds their first subdomain under
-- an apex (handled in the service layer). The user can also pre-register
-- a zone via the new /dns-zones UI to set up wildcard ahead of time.
--
-- Apex domains usable as app targets: the apex itself (e.g.
-- `meudominio.com`) can be added to `domains` like any subdomain — there's
-- no DB-level "is this an apex?" check, the service decides.

CREATE TABLE dns_zones (
    id                    TEXT PRIMARY KEY,
    apex                  TEXT NOT NULL UNIQUE,
    -- IP we expect every A record under this zone to point at. Cached on
    -- the zone so successive DNS checks don't re-probe the public IP.
    target_ip             TEXT,

    -- Apex (the bare `meudominio.com` A record) verification.
    apex_verified         INTEGER NOT NULL DEFAULT 0,
    apex_verified_at      INTEGER,
    apex_last_check       INTEGER,

    -- Wildcard (`*.meudominio.com`) verification. We can't query the
    -- wildcard record directly (DNS doesn't expose it), so the service
    -- probes a random subdomain (`prxprobe-XXXX.apex`) and checks whether
    -- it resolves to target_ip. A positive result means there IS a
    -- wildcard A/CNAME upstream.
    wildcard_verified     INTEGER NOT NULL DEFAULT 0,
    wildcard_verified_at  INTEGER,
    wildcard_last_check   INTEGER,

    -- Free-form notes from the operator (e.g. "Cloudflare zone, proxied").
    notes                 TEXT,

    created_at            INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at            INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TRIGGER trg_dns_zones_updated_at
AFTER UPDATE ON dns_zones FOR EACH ROW
BEGIN
    UPDATE dns_zones SET updated_at = unixepoch() WHERE id = OLD.id;
END;

ALTER TABLE domains ADD COLUMN zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL;
-- Denormalized: "the parent zone's wildcard probe is positive, so even
-- if my individual A check flakes I'm reachable via the wildcard". The
-- value is refreshed by the DNS check loop, not on every domain read.
ALTER TABLE domains ADD COLUMN covered_by_wildcard INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_domains_zone_id ON domains(zone_id);
