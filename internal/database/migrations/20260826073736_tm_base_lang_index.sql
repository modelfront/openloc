-- +goose Up
-- +goose StatementBegin

-- TM lookups now match language on the base subtag (a project sourced in en-us
-- can hit an `en` entry, and vice versa), so the predicate on the exact-match
-- path changed from `source_language = $2` to
-- `split_part(source_language, '-', 1) = split_part($2, '-', 1)`.
--
-- Migration 017 added idx_tm_exact_match for exactly this lookup, after
-- unindexed exact matching turned a 12K-segment pre-translate into a 6+ minute
-- job. A btree on the raw column can't serve the split_part predicate, so it's
-- replaced by an index on the expression. split_part is IMMUTABLE, so it's
-- indexable.
--
-- On production with a large translation_memory, run manually first to avoid
-- the table lock (goose wraps migrations in a transaction, so CONCURRENTLY
-- can't live in here):
--   CREATE INDEX CONCURRENTLY idx_tm_exact_match_base
--     ON translation_memory (tm_id, split_part(source_language, '-', 1), source_text);
CREATE INDEX IF NOT EXISTS idx_tm_exact_match_base
  ON translation_memory (tm_id, split_part(source_language, '-', 1), source_text);

-- Nothing queries the raw-column form any more; keeping both would double the
-- write amplification on every TM insert.
DROP INDEX IF EXISTS idx_tm_exact_match;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_tm_exact_match
  ON translation_memory (tm_id, source_language, source_text);
DROP INDEX IF EXISTS idx_tm_exact_match_base;
-- +goose StatementEnd
