-- +goose Up
-- Rich text columns for preserving XLIFF inline tags and HTML formatting.
-- source_rich_text: source text with inline XLIFF tags (bpt, ept, ph, etc.)
-- human_rich_text: human-edited translation with XLIFF tags or HTML formatting
-- Both are nullable — only set when content has tags/formatting.
-- The existing source_text and human_text columns remain plain text (for search, display, word count).
ALTER TABLE segments ADD COLUMN source_rich_text TEXT;
ALTER TABLE translations ADD COLUMN human_rich_text TEXT;

-- +goose Down
ALTER TABLE segments DROP COLUMN IF EXISTS source_rich_text;
ALTER TABLE translations DROP COLUMN IF EXISTS human_rich_text;
