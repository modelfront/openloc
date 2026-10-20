-- +goose Up

-- Remove unique constraint entirely. Every TMX <tu> entry should be stored as-is.
-- Same source + same target from different TUs = valid (different context, creator, date).
-- Deduplication is a separate user-initiated action (a "Remove duplicates" tool),
-- not something that silently happens during import.
ALTER TABLE translation_memory DROP CONSTRAINT IF EXISTS translation_memory_no_exact_dupes;

-- +goose Down
-- Re-add the hash-based constraint (from migration 011)
ALTER TABLE translation_memory ADD CONSTRAINT translation_memory_no_exact_dupes
  UNIQUE (tm_id, source_language, target_language, source_hash, target_hash);
