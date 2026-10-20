-- ============================================================
-- Term Bases (terminology containers)
-- ============================================================

-- name: ListTermBases :many
-- created_by_name is the creator's display name, resolved here so a list row
-- arrives ready to render (same pattern as ListConcepts below). Precedence:
--   1. users.name
--   2. users.email  — when the name is blank (the column defaults to an empty
--                     string and signup/SSO paths don't always set one)
--   3. blank        — when there is no user at all: created_by is NULL (row
--                     predates attribution) or the user was deleted
SELECT b.*,
  COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM term_bases b
LEFT JOIN users u ON u.id = b.created_by
WHERE b.tenant_id = $1 AND b.deleted_at IS NULL
ORDER BY b.name;

-- name: GetTermBase :one
SELECT * FROM term_bases
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateTermBase :one
-- target_languages is left at its '{}' default and derived from terms via
-- SyncTermBaseTargetLanguages as terms are added/imported.
INSERT INTO term_bases (tenant_id, name, description, source_language, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SyncTermBaseTargetLanguages :exec
-- Recompute a base's target_languages from its live terms (distinct languages
-- minus the source). Called after any term create/delete/import.
UPDATE term_bases b SET target_languages = COALESCE((
  SELECT array_agg(DISTINCT t.language ORDER BY t.language)
  FROM term t JOIN term_concept c ON c.id = t.concept_id
  WHERE c.term_base_id = b.id
    AND t.deleted_at IS NULL AND c.deleted_at IS NULL
    AND t.language <> b.source_language
), '{}'), updated_at = now()
WHERE b.id = $1;

-- name: UpdateTermBase :one
UPDATE term_bases
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTermBase :exec
-- Internal: hard delete (cascades to concepts+terms). Kept for admin/purge; the
-- UI path soft-deletes via SoftDeleteTermBase + the cascades below.
DELETE FROM term_bases WHERE id = $1;

-- name: SoftDeleteTermBase :exec
UPDATE term_bases SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteTermBaseConcepts :exec
-- Cascade a base soft-delete to its concepts (set-based, one statement).
UPDATE term_concept SET deleted_at = now(), updated_at = now()
WHERE term_base_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteTermBaseTerms :exec
-- Cascade a base soft-delete to its terms via the concept join. Independent of
-- concept.deleted_at so ordering vs. SoftDeleteTermBaseConcepts doesn't matter.
UPDATE term t SET deleted_at = now(), updated_at = now()
FROM term_concept c
WHERE t.concept_id = c.id AND c.term_base_id = $1 AND t.deleted_at IS NULL;

-- name: CountTermBaseEntries :one
-- The browser is concept-centric, so a base's headline count is its number of
-- live concepts (not target-language terms of the transitional default pair,
-- which is 0 for a multilingual base with no term in that language).
SELECT count(*)
FROM term_concept
WHERE term_base_id = $1 AND deleted_at IS NULL;

-- ============================================================
-- Concept + Term writes (backing the concept browser CRUD + TBX/CSV import).
-- ============================================================

-- name: CreateTermConcept :one
INSERT INTO term_concept (term_base_id, definition, domain, created_by, updated_by)
VALUES (@term_base_id, @definition, @domain, @created_by, @updated_by)
RETURNING *;

-- name: CreateTerm :one
INSERT INTO term (concept_id, language, text, status, part_of_speech, notes, mt_translation, created_by, updated_by)
VALUES (@concept_id, @language, @text, @status, @part_of_speech, @notes, @mt_translation, @created_by, @updated_by)
RETURNING *;

-- name: GetTerm :one
SELECT * FROM term WHERE id = $1;

-- name: FindConceptTerm :one
-- A live term in a concept with the given language + exact text. Backs the
-- add-term dedup guard (a concept never holds two identical terms in a language).
SELECT * FROM term
WHERE concept_id = @concept_id AND language = @language AND text = @text AND deleted_at IS NULL
ORDER BY id LIMIT 1;

-- name: SoftDeleteTerm :exec
-- Soft delete: hide the term everywhere but keep the row for audit/restore.
UPDATE term SET deleted_at = now(), updated_by = @updated_by, updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: CountConceptTermsInLang :one
-- Live terms only — drives the "soft-delete the concept too" decision.
SELECT count(*) FROM term
WHERE concept_id = @concept_id AND language = @language AND deleted_at IS NULL;

-- name: SoftDeleteTermConcept :exec
UPDATE term_concept SET deleted_at = now(), updated_by = @updated_by, updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- ============================================================
-- Concept-centric browser (Phase 4 concept editor). Reads list concepts with a
-- keyset cursor + search over any term text; writes go through the primitives
-- above plus the two updates below.
-- ============================================================

-- name: ListConceptsPage :many
-- One page of live concepts for a base (keyset by id). search '' = no filter;
-- otherwise matches domain, definition, or ANY live term's text. langs empty =
-- no filter; otherwise only concepts with a live term in ANY of the languages.
SELECT c.id, c.definition, c.domain, c.created_at, c.updated_at,
       COALESCE(cu.name, '')::text AS created_by_name,
       COALESCE(uu.name, '')::text AS updated_by_name
FROM term_concept c
LEFT JOIN users cu ON cu.id = c.created_by
LEFT JOIN users uu ON uu.id = c.updated_by
WHERE c.term_base_id = @term_base_id
  AND c.deleted_at IS NULL
  AND c.id > @cursor
  AND (
    @search::text = ''
    OR c.domain ILIKE '%' || @search || '%'
    OR c.definition ILIKE '%' || @search || '%'
    OR EXISTS (
      SELECT 1 FROM term t
      WHERE t.concept_id = c.id AND t.deleted_at IS NULL
        AND t.text ILIKE '%' || @search || '%'
        -- When a language filter is active, only match term text in those
        -- languages (search scoped to the selected languages' terms).
        AND (COALESCE(cardinality(@langs::text[]), 0) = 0 OR t.language = ANY(@langs::text[]))
    )
  )
  AND (
    COALESCE(cardinality(@langs::text[]), 0) = 0
    OR EXISTS (
      SELECT 1 FROM term tl
      WHERE tl.concept_id = c.id AND tl.deleted_at IS NULL AND tl.language = ANY(@langs::text[])
    )
  )
ORDER BY c.id
LIMIT @page_limit;

-- name: CountConcepts :one
SELECT count(*) FROM term_concept c
WHERE c.term_base_id = @term_base_id
  AND c.deleted_at IS NULL
  AND (
    @search::text = ''
    OR c.domain ILIKE '%' || @search || '%'
    OR c.definition ILIKE '%' || @search || '%'
    OR EXISTS (
      SELECT 1 FROM term t
      WHERE t.concept_id = c.id AND t.deleted_at IS NULL
        AND t.text ILIKE '%' || @search || '%'
        -- When a language filter is active, only match term text in those
        -- languages (search scoped to the selected languages' terms).
        AND (COALESCE(cardinality(@langs::text[]), 0) = 0 OR t.language = ANY(@langs::text[]))
    )
  )
  AND (
    COALESCE(cardinality(@langs::text[]), 0) = 0
    OR EXISTS (
      SELECT 1 FROM term tl
      WHERE tl.concept_id = c.id AND tl.deleted_at IS NULL AND tl.language = ANY(@langs::text[])
    )
  );

-- name: ListTermsForConcepts :many
-- All live terms for a set of concepts, ordered so callers can group by concept.
-- Joins users for the creator/updater display names (concept dialog stamps).
SELECT t.id, t.concept_id, t.language, t.text, t.status, t.part_of_speech, t.notes,
       t.created_at, t.updated_at,
       COALESCE(cu.name, '')::text AS created_by_name,
       COALESCE(uu.name, '')::text AS updated_by_name
FROM term t
LEFT JOIN users cu ON cu.id = t.created_by
LEFT JOIN users uu ON uu.id = t.updated_by
WHERE t.concept_id = ANY(@concept_ids::bigint[]) AND t.deleted_at IS NULL
ORDER BY t.concept_id, t.language, t.id;

-- name: GetConcept :one
-- Base-scoped (tenant verified by the handler's GetTermBase check).
SELECT * FROM term_concept
WHERE id = @id AND term_base_id = @term_base_id AND deleted_at IS NULL;

-- name: UpdateTermConcept :exec
UPDATE term_concept
SET definition = @definition, domain = @domain, updated_by = @updated_by, updated_at = now()
WHERE id = @id;

-- name: FindConceptsByTerm :many
-- Concepts in a base that contain a term with EXACT text (case-sensitive, no
-- ILIKE), optionally narrowed to an exact language and/or exact domain. Backs the
-- MCP find_concepts lookup: a model resolves a concept_id from a known term
-- without needing a project the base is assigned to. filter_domain=false ignores
-- the domain arg entirely; =true matches c.domain exactly (including '').
SELECT c.id, c.definition, c.domain
FROM term_concept c
WHERE c.term_base_id = @term_base_id
  AND c.deleted_at IS NULL
  AND (@filter_domain::bool = false OR c.domain = @domain)
  AND EXISTS (
    SELECT 1 FROM term t
    WHERE t.concept_id = c.id AND t.deleted_at IS NULL
      AND t.text = @text
      AND (@language::text = '' OR t.language = @language)
  )
ORDER BY c.id
LIMIT @row_limit;

-- name: FindConceptBySourceTerm :one
-- Find a live concept in a base whose source-language term matches (exact text)
-- AND whose domain matches. This is the concept identity used to MERGE a new
-- target term into an existing meaning instead of spawning a duplicate concept
-- (add_term calling twice for the same source term, different target languages).
-- Homographs stay distinct: a differing domain is a different concept.
SELECT c.* FROM term_concept c
JOIN term t ON t.concept_id = c.id AND t.deleted_at IS NULL
WHERE c.term_base_id = @term_base_id
  AND c.deleted_at IS NULL
  AND c.domain = @domain
  AND t.language = @language
  AND t.text = @text
ORDER BY c.id
LIMIT 1;

-- name: UpdateTermFull :exec
-- Concept-editor term update: text + status + part-of-speech + notes.
UPDATE term
SET text = @text, status = @status, part_of_speech = @part_of_speech,
    notes = @notes, updated_by = @updated_by, updated_at = now()
WHERE id = @id;

-- name: ListTermBaseLanguages :many
-- Distinct languages with at least one live term in the base. Drives the CSV
-- export language selector (the base is multilingual, so this is a superset of
-- the base's default source/target pair).
SELECT DISTINCT t.language
FROM term t
JOIN term_concept c ON c.id = t.concept_id
WHERE c.term_base_id = @term_base_id
  AND t.deleted_at IS NULL
  AND c.deleted_at IS NULL
ORDER BY t.language;

-- name: ListConceptTermsForExport :many
-- All live concepts + their live terms for a base, ordered so a streaming writer
-- can group by concept. Drives the multilingual TBX export.
SELECT c.id AS concept_id, c.definition, c.domain,
       t.language, t.text, t.status, t.part_of_speech, t.notes
FROM term_concept c
JOIN term t ON t.concept_id = c.id AND t.deleted_at IS NULL
WHERE c.term_base_id = @term_base_id AND c.deleted_at IS NULL
ORDER BY c.id, t.language, t.id;

-- name: FindTenantMTEngine :one
-- Any enabled MT engine in the tenant. Used by seed (attribute dashboard history).
SELECT * FROM mt_engines
WHERE tenant_id = @tenant_id AND enabled = true AND deleted_at IS NULL
ORDER BY id
LIMIT 1;

-- name: CountTermBaseUsage :one
-- How many projects and templates have this base assigned. Tenant-scoped
-- through settings_profiles.tenant_id. Feeds the delete preview.
SELECT count(DISTINCT p.id)::int AS project_count,
       count(DISTINCT t.id)::int AS template_count
FROM settings_terminology_assignments a
JOIN settings_profiles sp ON sp.id = a.settings_profile_id
LEFT JOIN projects p ON p.settings_profile_id = sp.id AND p.deleted_at IS NULL
LEFT JOIN templates t ON t.settings_profile_id = sp.id
WHERE a.term_base_id = @term_base_id AND sp.tenant_id = @tenant_id;
