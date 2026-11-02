-- name: ListEditorIndex :many
-- One row per segment of a job, in document order, without the text the grid
-- renders: what the CAT editor counts, filters and navigates on. A job's rows
-- are the segments that own a translation in its language, so a shared segment
-- a split or join replaced in this language is left out. The source text is
-- read only to find repetitions, and is not sent on. Tenant scoping is
-- structural via files→projects.
SELECT
    s.id                        AS segment_id,
    s.position                  AS position,
    s.sub_position              AS sub_position,
    s.word_count                AS word_count,
    s.source_text               AS source_text,
    t.id                        AS translation_id,
    t.status                    AS status,
    COALESCE(t.active_text, '') <> '' AS has_target,
    COALESCE(t.step_edited, false)::bool AS step_edited,
    t.predict_outcome           AS predict_outcome,
    t.origin                    AS origin,
    t.tm_match_percent          AS tm_match_percent,
    COALESCE(t.locked, false)::bool AS locked,
    t.in_flight_phase           AS in_flight_phase,
    COALESCE(t.confirmed_via, '')::text AS confirmed_via,
    -- Six characters say whether the note is short enough to show whole on
    -- the QE chip (five or fewer) without sending a long vendor note.
    COALESCE(left(t.qe_note, 6), '')::text AS qe_note_head,
    -- Read to tell which rows a join may be offered on; not sent.
    COALESCE(s.export_key, s.segment_key, '')::text AS unit_key,
    f.format AS file_format
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
ORDER BY s.position, s.sub_position;

-- name: ListEditorSegmentsByIDs :many
-- The segments of @ids that belong to the job's file and language; others are
-- not returned.
SELECT s.* FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND (s.target_language IS NULL OR s.target_language = sqlc.arg(target_language)::text)
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND s.id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY s.position, s.sub_position;

-- name: ListEditorTranslationsBySegments :many
-- Internal: the translations of @segment_ids in one language. The ids come from
-- ListEditorSegmentsByIDs (tenant-scoped), which is the tenant scope here.
SELECT t.* FROM translations t
WHERE t.segment_id = ANY(sqlc.arg(segment_ids)::bigint[])
  AND t.target_language = sqlc.arg(target_language)::text;

-- name: ListEditorPreviousStepTexts :many
-- For each of @translation_ids, the text it held when its most recently closed
-- step round ended: the newest history text at or before the round's end,
-- skipping `cleared` and the history a join moved here from the rows it
-- removed (details.from_segment), which is another row's text. The step
-- handoff and a reject write the text a person left unconfirmed, so history
-- holds every handed-over version. A translation with no closed round, or no
-- text when it ended, is not returned.
WITH prev AS (
    SELECT DISTINCT ON (tss.translation_id) tss.translation_id, tss.ended_at
    FROM translation_step_statuses tss
    WHERE tss.translation_id = ANY(sqlc.arg(translation_ids)::bigint[])
      AND tss.tenant_id = sqlc.arg(tenant_id)::bigint
      AND tss.ended_at IS NOT NULL
    ORDER BY tss.translation_id, tss.ended_at DESC, tss.id DESC
)
SELECT prev.translation_id, h.text::text AS text
FROM prev
JOIN LATERAL (
    SELECT th.text FROM translation_history th
    WHERE th.translation_id = prev.translation_id
      AND th.text IS NOT NULL
      AND NOT COALESCE(th.details ? 'from_segment', false)
      AND th.created_at <= prev.ended_at
    ORDER BY th.created_at DESC, th.id DESC
    LIMIT 1
) h ON true;

-- name: ListEditorTexts :many
-- Source and target text of every segment of a job, for the editor's text
-- search.
SELECT
    s.id          AS segment_id,
    s.source_text AS source_text,
    COALESCE(t.active_text, '')::text AS target_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
ORDER BY s.position, s.sub_position;

-- name: ListEditorRefSources :many
-- Tagged sources of a job's segments that carry a cross-reference chip.
SELECT s.source_rich_text::text AS source_rich_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND s.source_rich_text LIKE '%ref\_key="%';

-- name: ListEditorSegmentsByKeys :many
-- The job's segments with these original keys, and their translation. A key
-- a split replaced in this language comes back once per piece, keyed by the
-- original key, in document order.
SELECT
    s.id          AS segment_id,
    COALESCE(s.export_key, s.segment_key)::text AS segment_key,
    s.source_text AS source_text,
    COALESCE(t.active_text, '')::text AS target_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND COALESCE(s.export_key, s.segment_key) = ANY(sqlc.arg(keys)::text[])
ORDER BY s.position, s.sub_position;

-- name: ListEditorWordCandidates :many
-- Translations of a job's file whose target contains @word anywhere (case
-- sensitive). The caller narrows to whole words.
SELECT t.id, t.segment_id, t.locked, t.active_text, t.active_rich_text, t.updated_at
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = sqlc.arg(file_id)::bigint
  AND t.target_language = sqlc.arg(target_language)::text
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND strpos(t.active_text, sqlc.arg(word)::text) > 0
ORDER BY s.position, s.sub_position;

-- name: GetEditorSourceLanguage :one
-- The source language of a file's project: how the editor reads the
-- numbers and dates of its placeables. Tenant scoping is structural via
-- files→projects.
SELECT p.source_language
FROM files f
JOIN projects p ON p.id = f.project_id AND p.tenant_id = @tenant_id
WHERE f.id = @file_id;
