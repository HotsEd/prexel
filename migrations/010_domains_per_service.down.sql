DROP INDEX IF EXISTS domains_service_route_unique;
ALTER TABLE domains DROP COLUMN port;
ALTER TABLE domains DROP COLUMN service;
