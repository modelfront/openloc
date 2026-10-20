-- +goose Up
-- 101% context matches (in-context exact).
--
-- A TM entry records the md5 of the source text of the segments immediately
-- before/after it (document order) at write-back time. During pretranslation,
-- a byte-exact source hit whose stored neighbor hashes equal the current
-- segment's neighbor hashes scores tm_match_percent=101 instead of 100.
--
-- Nullable + no backfill: entries written before this migration (and TMX
-- imports, which carry no document context) keep NULL hashes and simply never
-- score 101% — they degrade to plain 100%. Storage is last-write-wins
-- (UpsertTMPipelineEntry), so no dedup/uniqueness change and no TM bloat.
-- Boundary neighbors hash the empty string (md5('')), so document-start/end
-- segments are 101%-eligible when the same boundary recurs.
ALTER TABLE translation_memory
    ADD COLUMN prev_source_hash text,
    ADD COLUMN next_source_hash text;

-- +goose Down
ALTER TABLE translation_memory
    DROP COLUMN IF EXISTS next_source_hash,
    DROP COLUMN IF EXISTS prev_source_hash;
