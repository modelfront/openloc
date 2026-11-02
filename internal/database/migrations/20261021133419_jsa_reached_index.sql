-- +goose NO TRANSACTION
-- +goose Up
-- The tasks that have reached their step, per step, with the job: what
-- ListReachedStepsForProfile counts on every project settings read. Partial,
-- so the many tasks still waiting on a step stay out of it.
--
-- CONCURRENTLY keeps writes flowing while the index builds, and cannot run
-- inside a transaction. A failed concurrent build leaves an invalid index
-- behind, so it is dropped first: a re-run after a partial failure builds it
-- again.
DROP INDEX CONCURRENTLY IF EXISTS idx_jsa_reached;
CREATE INDEX CONCURRENTLY idx_jsa_reached ON job_step_assignments (workflow_step_id) INCLUDE (job_id)
    WHERE status <> 'pending' OR round > 0;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_jsa_reached;
