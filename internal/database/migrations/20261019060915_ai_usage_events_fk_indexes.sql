-- +goose NO TRANSACTION
-- +goose Up
-- Indexes on the ON DELETE SET NULL foreign keys of ai_usage_events. Deleting
-- a job or an AI engine looks up the usage rows that point at it; without
-- these indexes each deleted parent row scans the whole table.
--
-- CONCURRENTLY keeps writes flowing while each index builds. A failed
-- concurrent build leaves an invalid index behind, so each one is dropped
-- first: a re-run after a partial failure builds it again.
DROP INDEX CONCURRENTLY IF EXISTS idx_ai_usage_events_job;
CREATE INDEX CONCURRENTLY idx_ai_usage_events_job
    ON ai_usage_events (job_id) WHERE job_id IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_ai_usage_events_ai_engine;
CREATE INDEX CONCURRENTLY idx_ai_usage_events_ai_engine
    ON ai_usage_events (ai_engine_id) WHERE ai_engine_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_ai_usage_events_job;
DROP INDEX CONCURRENTLY IF EXISTS idx_ai_usage_events_ai_engine;
