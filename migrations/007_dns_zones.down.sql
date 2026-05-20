-- SQLite cannot DROP COLUMN before 3.35; we rely on a recent version (the
-- project uses modernc.org/sqlite v1.x which embeds 3.40+). If the rollback
-- ever runs on an older binary, the ALTERs will fail loudly — acceptable
-- for a rollback path.
ALTER TABLE domains DROP COLUMN zone_id;
ALTER TABLE domains DROP COLUMN covered_by_wildcard;

DROP TRIGGER IF EXISTS trg_dns_zones_updated_at;
DROP TABLE IF EXISTS dns_zones;
