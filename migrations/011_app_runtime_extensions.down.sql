-- Down: drop everything added in 011_app_runtime_extensions.up.sql.
--
-- SQLite supports `ALTER TABLE … DROP COLUMN` since 3.35, which is
-- comfortably below our minimum (modernc.org/sqlite ships a much
-- newer engine). Down migrations only run in dev/test rollbacks —
-- production never invokes them — but we keep them honest.

DROP TRIGGER IF EXISTS trg_app_volumes_updated_at;
DROP INDEX  IF EXISTS idx_app_tags_tag;
DROP INDEX  IF EXISTS idx_app_volumes_app;
DROP TABLE  IF EXISTS app_tags;
DROP TABLE  IF EXISTS tags;
DROP TABLE  IF EXISTS app_volumes;

ALTER TABLE domains DROP COLUMN force_https;

ALTER TABLE apps DROP COLUMN docker_labels;
ALTER TABLE apps DROP COLUMN limits_cpu_shares;
ALTER TABLE apps DROP COLUMN limits_cpuset;
ALTER TABLE apps DROP COLUMN limits_memory_reservation;
ALTER TABLE apps DROP COLUMN limits_memory_swappiness;
ALTER TABLE apps DROP COLUMN limits_memory_swap;
ALTER TABLE apps DROP COLUMN build_args_source_commit;
ALTER TABLE apps DROP COLUMN build_args_inject;
ALTER TABLE apps DROP COLUMN auto_deploy_branch;
ALTER TABLE apps DROP COLUMN restart_policy;
ALTER TABLE apps DROP COLUMN post_deploy_command;
ALTER TABLE apps DROP COLUMN pre_deploy_command;
