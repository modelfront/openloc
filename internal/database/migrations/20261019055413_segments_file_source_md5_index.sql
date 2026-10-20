-- +goose NO TRANSACTION
-- +goose Up
-- The QA consistency check finds the other segments of a file with the same
-- source text. md5 keeps the index entries small for long sources; queries
-- compare md5(source_text) and then the text itself.
--
-- CONCURRENTLY builds the index without blocking writes to segments. A failed
-- concurrent build leaves an INVALID index behind, so the DROP first makes a
-- re-run safe.
DROP INDEX CONCURRENTLY IF EXISTS idx_segments_file_source_md5;
CREATE INDEX CONCURRENTLY idx_segments_file_source_md5 ON segments (file_id, md5(source_text));

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_segments_file_source_md5;
