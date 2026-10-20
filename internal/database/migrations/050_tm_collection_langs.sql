-- +goose Up
-- A TM now captures a source language, one or more target languages,
-- and an optional customer at creation time (parity with project creation).
--
-- Columns are NULLABLE on purpose: legacy TMs predate these fields and there's
-- no meaningful value to backfill (TMs were language-agnostic containers). The
-- API enforces source_language + at least one target_language as REQUIRED for
-- new creates; existing rows keep NULL. customer_id mirrors projects.customer_id
-- (FK, ON DELETE SET NULL, partial index on tenant scope).
ALTER TABLE tm_collections
  ADD COLUMN source_language  text,
  ADD COLUMN target_languages text[],
  ADD COLUMN customer_id      bigint REFERENCES customers(id) ON DELETE SET NULL;

CREATE INDEX idx_tm_collections_customer
  ON tm_collections (tenant_id, customer_id)
  WHERE customer_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_tm_collections_customer;
ALTER TABLE tm_collections
  DROP COLUMN IF EXISTS customer_id,
  DROP COLUMN IF EXISTS target_languages,
  DROP COLUMN IF EXISTS source_language;
