-- +goose NO TRANSACTION
-- +goose Up
-- Indexes on the ON DELETE SET NULL foreign keys of translation_history.
-- Deleting a step assignment, TM entry, TM or MT engine looks up the history
-- rows that point at it; without these indexes each deleted parent row scans
-- the whole table. The per-step productivity report also starts from a
-- project's step assignments through the first index.
--
-- CONCURRENTLY keeps writes flowing while each index builds, and cannot run
-- inside a transaction. A failed concurrent build leaves an invalid index
-- behind, so each one is dropped first: a re-run after a partial failure
-- builds it again.
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_job_step_assignment;
CREATE INDEX CONCURRENTLY idx_translation_history_job_step_assignment
    ON translation_history (job_step_assignment_id) WHERE job_step_assignment_id IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tm_entry;
CREATE INDEX CONCURRENTLY idx_translation_history_tm_entry
    ON translation_history (tm_entry_id) WHERE tm_entry_id IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tm;
CREATE INDEX CONCURRENTLY idx_translation_history_tm
    ON translation_history (tm_id) WHERE tm_id IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_mt_engine;
CREATE INDEX CONCURRENTLY idx_translation_history_mt_engine
    ON translation_history (mt_engine_id) WHERE mt_engine_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_job_step_assignment;
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tm_entry;
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tm;
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_mt_engine;
