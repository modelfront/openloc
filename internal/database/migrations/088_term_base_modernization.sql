-- +goose Up
-- Modernize term_bases for the concept model:
--   * soft delete (deleted_at) — deleting a base with tens of thousands of
--     concepts/terms as a hard cascade was slow; a soft delete is set-based and
--     keeps the rows for audit/restore.
--   * one source_language + derived target_languages[] instead of a fixed
--     source→target pair (meaningless for a multilingual base). target_languages
--     is maintained from the base's actual term languages (minus the source).
--   * retire terminology_entry_view (the pair-era bilingual projection) — the
--     concept browser + concept-native queries replaced it.
ALTER TABLE term_bases ADD COLUMN deleted_at timestamptz;
CREATE INDEX idx_term_bases_tenant_live ON term_bases (tenant_id) WHERE deleted_at IS NULL;

ALTER TABLE term_bases ADD COLUMN target_languages text[] NOT NULL DEFAULT '{}';
UPDATE term_bases b SET target_languages = COALESCE((
  SELECT array_agg(DISTINCT t.language ORDER BY t.language)
  FROM term t JOIN term_concept c ON c.id = t.concept_id
  WHERE c.term_base_id = b.id
    AND t.deleted_at IS NULL AND c.deleted_at IS NULL
    AND t.language <> b.source_language
), '{}');

-- Drop the view BEFORE the column it depends on.
DROP VIEW IF EXISTS terminology_entry_view;
ALTER TABLE term_bases DROP COLUMN target_language;

-- +goose Down
-- Restore the post-087 world: single target_language + the 087 view, no soft delete.
ALTER TABLE term_bases ADD COLUMN target_language text NOT NULL DEFAULT '';
UPDATE term_bases SET target_language = COALESCE(target_languages[1], 'en');

CREATE VIEW terminology_entry_view AS
SELECT tt.id,
       c.term_base_id,
       COALESCE((SELECT s.text FROM term s
                 WHERE s.concept_id = c.id AND s.language = b.source_language
                   AND s.deleted_at IS NULL
                 ORDER BY s.id LIMIT 1), '')::text AS source_term,
       tt.text AS target_term,
       tt.status,
       tt.notes,
       c.domain,
       tt.created_by,
       tt.created_at,
       tt.updated_at,
       tt.mt_translation
FROM term tt
JOIN term_concept c ON c.id = tt.concept_id
JOIN term_bases b ON b.id = c.term_base_id
WHERE tt.language = b.target_language
  AND tt.deleted_at IS NULL
  AND c.deleted_at IS NULL;

ALTER TABLE term_bases DROP COLUMN target_languages;
DROP INDEX IF EXISTS idx_term_bases_tenant_live;
ALTER TABLE term_bases DROP COLUMN deleted_at;
