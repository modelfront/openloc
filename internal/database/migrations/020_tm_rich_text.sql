-- +goose Up
-- +goose StatementBegin

-- Add rich-text columns to translation_memory, mirroring the segments +
-- translations rich_text pattern (migrations 008 + 013). The plain
-- source_text/target_text columns stay authoritative for matching, dedup
-- (via md5 hash), and search — markup must NOT influence match score or
-- duplicate detection. The new rich_text columns hold the inline-tag-
-- bearing version for DISPLAY ONLY — TM browser, concordance, etc.
--
-- Nullable: TMs imported before this migration have NULL rich_text and
-- continue to render via the plain text fallback. Frontend prefers
-- rich_text when present (parser → atomic chips), falls back to plain.
--
-- Schema-only fix for the bug where the Concordance modal and TM browser
-- show raw <bpt i="1">…</bpt> markup (R24 in the QA feedback sheet).
-- The data path is updated in tandem: TMX importer extracts both forms,
-- pipeline write-back stores the segment's source_rich_text + the
-- translation's human_rich_text.

ALTER TABLE translation_memory ADD COLUMN source_rich_text TEXT;
ALTER TABLE translation_memory ADD COLUMN target_rich_text TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE translation_memory DROP COLUMN IF EXISTS target_rich_text;
ALTER TABLE translation_memory DROP COLUMN IF EXISTS source_rich_text;

-- +goose StatementEnd
