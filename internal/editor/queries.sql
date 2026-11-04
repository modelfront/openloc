-- name: ListEditorIndex :many
-- One row per segment of the editor's scope (the files of its jobs, in the
-- order of @file_ids, each in document order), without the text the grid
-- renders: what the CAT editor counts, filters and navigates on. A job's rows
-- are the segments that own a translation in its language, so a shared segment
-- a split or join replaced in this language is left out. The source text is
-- read only to find repetitions, and is not sent on. Tenant scoping is
-- structural via files→projects.
SELECT
    s.id                        AS segment_id,
    s.file_id                   AS file_id,
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
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
ORDER BY array_position(sqlc.arg(file_ids)::bigint[], s.file_id), s.position, s.sub_position;

-- name: ListEditorSegmentsByIDs :many
-- The segments of @ids that belong to the scope's files and language; others
-- are not returned.
SELECT s.* FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND (s.target_language IS NULL OR s.target_language = sqlc.arg(target_language)::text)
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND s.id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY array_position(sqlc.arg(file_ids)::bigint[], s.file_id), s.position, s.sub_position;

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
-- Source and target text of every segment of the scope, for the editor's
-- text search.
SELECT
    s.id          AS segment_id,
    s.source_text AS source_text,
    COALESCE(t.active_text, '')::text AS target_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
ORDER BY array_position(sqlc.arg(file_ids)::bigint[], s.file_id), s.position, s.sub_position;

-- name: ListEditorRefSources :many
-- Tagged sources of the scope's segments that carry a cross-reference chip,
-- with their file: a chip points at a key in its own file.
SELECT s.file_id AS file_id, s.source_rich_text::text AS source_rich_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND s.source_rich_text LIKE '%ref\_key="%';

-- name: ListEditorSegmentsByKeys :many
-- The scope's segments with these original keys, and their translation. A
-- key a split replaced in this language comes back once per piece, keyed by
-- the original key, in document order. The caller keeps the keys each file's
-- chips name.
SELECT
    s.id          AS segment_id,
    s.file_id     AS file_id,
    COALESCE(s.export_key, s.segment_key)::text AS segment_key,
    s.source_text AS source_text,
    COALESCE(t.active_text, '')::text AS target_text
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND COALESCE(s.export_key, s.segment_key) = ANY(sqlc.arg(keys)::text[])
ORDER BY array_position(sqlc.arg(file_ids)::bigint[], s.file_id), s.position, s.sub_position;

-- name: ListEditorWordCandidates :many
-- Translations of the scope's files whose target contains @word anywhere
-- (case sensitive). The caller narrows to whole words.
SELECT t.id, t.segment_id, t.locked, t.active_text, t.active_rich_text, t.updated_at
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND t.target_language = sqlc.arg(target_language)::text
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND strpos(t.active_text, sqlc.arg(word)::text) > 0
ORDER BY array_position(sqlc.arg(file_ids)::bigint[], s.file_id), s.position, s.sub_position;

-- name: GetEditorSourceLanguage :one
-- The source language of a file's project: how the editor reads the
-- numbers and dates of its placeables. Tenant scoping is structural via
-- files→projects.
SELECT p.source_language
FROM files f
JOIN projects p ON p.id = f.project_id AND p.tenant_id = @tenant_id
WHERE f.id = @file_id;

-- name: ListEditorScopeJobs :many
-- The jobs of @job_ids in the caller's tenant, with their file and the step in
-- progress (none: empty name): what an editor scope opens. A job of a deleted
-- project is left out; the caller checks that every id came back. Tenant
-- scoping is structural via files→projects.
SELECT j.id AS job_id, j.file_id, j.target_language, f.project_id,
       f.name AS file_name, f.format AS file_format, f.inline_html,
       COALESCE(cur.name, '')::text AS step_name,
       cur.assigned_to AS step_assignee,
       COALESCE(cur.id, 0)::bigint AS step_id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN LATERAL (
    SELECT jsa.id, ws.name, jsa.assigned_to
    FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND jsa.status = 'in_progress'
    ORDER BY ws.step_order
    LIMIT 1
) cur ON true
WHERE j.id = ANY(sqlc.arg(job_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL;

-- name: ListJobsWithPreviousStep :many
-- The jobs of @job_ids with a finished step round on any of their segments:
-- the editor's track changes has a baseline to mark against. Tenant scoping
-- is structural via files→projects.
SELECT j.id AS job_id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = ANY(sqlc.arg(job_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND EXISTS (
    SELECT 1
    FROM job_step_assignments jsa
    JOIN translation_step_statuses tss ON tss.job_step_assignment_id = jsa.id
    WHERE jsa.job_id = j.id AND tss.ended_at IS NOT NULL
  );

-- name: CountEditorComments :many
-- Open comments per translation of the scope, for the grid's comment marks.
-- Translations without a comment are left out.
SELECT t.id AS translation_id, COUNT(sc.id)::int AS comment_count
FROM translations t
JOIN segment_comments sc ON sc.translation_id = t.id AND sc.deleted_at IS NULL
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND t.target_language = sqlc.arg(target_language)::text
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
GROUP BY t.id;

-- name: ListEditorFuzzyCandidates :many
-- Internal fuzzy matches: confirmed translations in the scope's files whose
-- source looks like @query, best first. The segment itself and identical
-- sources (repetitions, which propagation fills) are left out. `%` compares
-- against pg_trgm.similarity_threshold: run it through editor.fuzzyCandidates,
-- which sets that first. The caller rescores with the TM scorer.
SELECT s.id AS segment_id, s.file_id, s.source_text, s.source_rich_text,
       t.id AS translation_id,
       COALESCE(t.active_text, '')::text AS target_text, t.active_rich_text AS target_rich_text,
       t.updated_at
FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = sqlc.arg(target_language)::text
WHERE s.file_id = ANY(sqlc.arg(file_ids)::bigint[])
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND t.status = 'approved'
  AND COALESCE(t.active_text, '') <> ''
  AND s.id <> sqlc.arg(segment_id)::bigint
  AND s.source_text <> sqlc.arg(query)::text
  AND s.source_text % sqlc.arg(query)::text
ORDER BY similarity(s.source_text, sqlc.arg(query)::text) DESC, s.id
LIMIT sqlc.arg(candidate_limit)::int;
