-- +goose Up

-- Allow multiple translations for the same source text in a TM.
-- A source may carry several stored targets; choosing between them is a lookup concern.
-- Old constraint only allowed ONE target per (tm_id, src_lang, tgt_lang, source_text).

ALTER TABLE translation_memory DROP CONSTRAINT translation_memory_tm_id_src_tgt_text_key;

-- Add hash columns for deduplication (B-tree can't index long text fields).
-- MD5 hash of source+target text used for unique constraint instead.
ALTER TABLE translation_memory ADD COLUMN source_hash text NOT NULL DEFAULT '';
ALTER TABLE translation_memory ADD COLUMN target_hash text NOT NULL DEFAULT '';

-- Backfill hashes for existing rows
UPDATE translation_memory SET source_hash = md5(source_text), target_hash = md5(target_text);

-- New constraint: same source AND same target (by hash) = true duplicate.
-- Different target for same source = valid context-dependent translation (keep).
ALTER TABLE translation_memory ADD CONSTRAINT translation_memory_no_exact_dupes
  UNIQUE (tm_id, source_language, target_language, source_hash, target_hash);

-- Metadata columns for TMX round-trip preservation
ALTER TABLE translation_memory ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE translation_memory ADD COLUMN created_by text NOT NULL DEFAULT '';
ALTER TABLE translation_memory ADD COLUMN updated_by text NOT NULL DEFAULT '';
ALTER TABLE translation_memory ADD COLUMN tu_id text NOT NULL DEFAULT '';
ALTER TABLE translation_memory ADD COLUMN usage_count integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE translation_memory DROP CONSTRAINT IF EXISTS translation_memory_no_exact_dupes;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS source_hash;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS target_hash;
ALTER TABLE translation_memory ADD CONSTRAINT translation_memory_tm_id_src_tgt_text_key
  UNIQUE (tm_id, source_language, target_language, source_text);
ALTER TABLE translation_memory DROP COLUMN IF EXISTS updated_at;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS created_by;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS updated_by;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS tu_id;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS usage_count;
