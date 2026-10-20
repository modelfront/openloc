-- +goose NO TRANSACTION
-- +goose Up
-- Trigram index on translation_memory.target_text, the twin of
-- idx_tm_source_trgm. TM find-and-replace filters target_text with LIKE or
-- ILIKE '%term%' when its scope is target or both; without this index that
-- reads every entry of the TM.
--
-- CONCURRENTLY keeps writes flowing while the index builds. A failed
-- concurrent build leaves an invalid index behind, so it is dropped first: a
-- re-run after a partial failure builds it again.
DROP INDEX CONCURRENTLY IF EXISTS idx_tm_target_trgm;
CREATE INDEX CONCURRENTLY idx_tm_target_trgm
    ON translation_memory USING gin (target_text gin_trgm_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_tm_target_trgm;
