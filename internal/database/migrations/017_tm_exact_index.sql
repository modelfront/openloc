-- +goose Up
-- Speeds up exact TM lookups during pre-translate. The existing idx_tm_lookup
-- covers (tm_id, source_language, target_language) but NOT source_text, forcing
-- a table scan for each LookupTM query. With 12K segments this means 24K queries
-- taking 6+ minutes. This index makes each lookup an index-only scan.
-- On production with large TMs, run manually with CONCURRENTLY to avoid table lock:
-- CREATE INDEX CONCURRENTLY idx_tm_exact_match ON translation_memory (tm_id, source_language, source_text);
CREATE INDEX IF NOT EXISTS idx_tm_exact_match
  ON translation_memory (tm_id, source_language, source_text);

-- +goose Down
DROP INDEX IF EXISTS idx_tm_exact_match;
