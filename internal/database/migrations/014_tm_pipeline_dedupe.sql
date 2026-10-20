-- +goose Up

-- Section 5.2: partial unique index for pipeline-written TM entries.
--
-- Background: migration 012 dropped the global unique constraint because
-- every imported TMX <tu> must be preserved as-is (same source + same
-- target from different <tu>s is valid — different context, creator, date).
--
-- But the PIPELINE write-back path (human confirming a segment in the CAT
-- editor via tm_write.go::SaveSegmentToTM) was doing a check-then-insert
-- dance that races under concurrent saves — two goroutines both see "no
-- match", both INSERT, and the TM ends up with duplicate rows.
--
-- Fix: add a partial unique index scoped to `created_by = 'human'`. This
-- enforces dedupe for pipeline writes while leaving TMX-imported rows
-- (where created_by is the TMX creator like "Trados", "memoQ", "SDL", etc.)
-- completely unaffected — they can still have as many duplicates as the
-- TMX file contains.
--
-- The conflict target on the upsert query is
--   (tm_id, source_language, target_language, source_hash, target_hash)
--   WHERE created_by = 'human'
-- and the WHERE clause MUST match this index's predicate exactly for
-- PostgreSQL's ON CONFLICT inference to pick this partial index.

CREATE UNIQUE INDEX translation_memory_pipeline_dedupe
  ON translation_memory (tm_id, source_language, target_language, source_hash, target_hash)
  WHERE created_by = 'human';

-- +goose Down
DROP INDEX IF EXISTS translation_memory_pipeline_dedupe;
