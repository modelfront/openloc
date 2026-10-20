-- +goose Up
-- Expression index for fast case-insensitive term lookup by token matching.
-- Replaces full table scan + ILIKE with O(log n) lookups per token.
CREATE INDEX idx_terminology_entries_source_term_lower
ON terminology_entries (lower(source_term));

-- GIN trigram index for future substring/similarity queries (concordance, fuzzy term search).
CREATE INDEX idx_terminology_entries_source_term_trgm
ON terminology_entries USING gin (source_term gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS idx_terminology_entries_source_term_trgm;
DROP INDEX IF EXISTS idx_terminology_entries_source_term_lower;
