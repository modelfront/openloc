-- name: CreateLanguageSegment :one
-- Internal: a segment that exists only in one language's job, made by a split
-- or join; the caller has checked the job in the tenant. Its key is
-- "<export key>@<id>": unique in the file, and the same for the row's life, so
-- a bilingual XLIFF exported before a later split still finds it.
INSERT INTO segments (id, file_id, target_language, source_text, source_rich_text,
                      segment_key, position, sub_position, context, word_count,
                      export_key, gap_before, chip_base)
OVERRIDING SYSTEM VALUE
SELECT n.id, @file_id, @target_language::text, @source_text, sqlc.narg(source_rich_text),
       @export_key::text || '@' || n.id, @position, @sub_position, @context::text, @word_count,
       @export_key::text, sqlc.narg(gap_before), @chip_base
FROM (SELECT nextval(pg_get_serial_sequence('segments', 'id')) AS id) n
RETURNING *;

-- name: ListJobEditRowsByTranslations :many
-- Tenant-scoped: the job's rows whose translation is one of @translation_ids,
-- in document order, with what a split or join reads. Rows of another job
-- are not returned.
SELECT s.id AS segment_id, s.target_language AS segment_language, s.source_text, s.source_rich_text,
       s.segment_key, s.export_key, s.position, s.sub_position, s.gap_before, s.chip_base,
       s.context, t.id AS translation_id, t.locked, t.active_text, t.active_rich_text,
       t.in_flight_phase, t.updated_at
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = @file_id AND t.target_language = @target_language
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND t.id = ANY(@translation_ids::bigint[])
ORDER BY s.position, s.sub_position;

-- name: ListJobEditRowsAt :many
-- Tenant-scoped: the job's rows at @position (a row and the pieces of its
-- split), in document order, with what a split or join reads.
SELECT s.id AS segment_id, s.target_language AS segment_language, s.source_text, s.source_rich_text,
       s.segment_key, s.export_key, s.position, s.sub_position, s.gap_before, s.chip_base,
       s.context, t.id AS translation_id, t.locked, t.active_text, t.active_rich_text,
       t.in_flight_phase, t.updated_at
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = @file_id AND t.target_language = @target_language
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND s.position = @position
ORDER BY s.position, s.sub_position;

-- name: GetSharedSegmentByKey :one
-- Internal: the file's shared segment with this key at this position; the
-- caller has checked the file in the tenant.
SELECT * FROM segments
WHERE file_id = @file_id AND target_language IS NULL
  AND segment_key = @segment_key AND position = @position
ORDER BY id
LIMIT 1;

-- name: MoveEditedTranslation :one
-- Internal: the caller has checked the job in the tenant. Moves a translation
-- onto the segment a split or join made (or back onto the shared segment) and
-- sets its text. The source changed, so a QE verdict and a TM score no longer
-- apply and the row needs review. Matches nothing when the row changed since
-- @expected_updated_at, is locked, is being pre-translated or another user
-- holds it.
UPDATE translations
SET segment_id = @segment_id,
    step_edited = step_edited OR active_text IS DISTINCT FROM sqlc.narg(active_text)::text,
    active_text = sqlc.narg(active_text)::text,
    active_rich_text = sqlc.narg(active_rich_text)::text,
    status = 'needs_review',
    predict_outcome = NULL,
    qe_note = NULL,
    tm_match_percent = NULL,
    edited_by = @actor_id,
    updated_at = now()
WHERE id = @id
  AND updated_at = @expected_updated_at
  AND NOT locked
  AND in_flight_phase IS NULL
  AND NOT translation_held_by_other(translations.id, @actor_id)
RETURNING *;

-- name: DeleteJoinedTranslations :exec
-- Internal: the translations a join folded into another row, after their
-- comments and history moved there. Their QA results, step statuses and holds
-- go with them. One statement, so the job's stats are recounted once.
DELETE FROM translations WHERE id = ANY(@ids::bigint[]);

-- name: DeleteLanguageSegments :exec
-- Internal: the language rows a split or join replaced, after their
-- translations moved off them; shared rows in @ids stay. One statement, so
-- the job's stats are recounted once.
DELETE FROM segments WHERE id = ANY(@ids::bigint[]) AND target_language IS NOT NULL;

-- name: RenumberLanguageRows :exec
-- Internal: numbers one job's language rows at @position 0, 1, 2… in their
-- order (sub-position, then the order they were made).
UPDATE segments s
SET sub_position = r.n
FROM (
    SELECT ls.id, (row_number() OVER (ORDER BY ls.sub_position, ls.id) - 1)::int AS n
    FROM segments ls
    WHERE ls.file_id = @file_id AND ls.target_language = @target_language::text
      AND ls.position = @position
) r
WHERE s.id = r.id AND s.sub_position <> r.n;

-- name: MoveSegmentComments :exec
-- Internal: a join moves the comments of the row it removes to the row it
-- keeps, each labelled with the number of the row it was written on.
UPDATE segment_comments
SET translation_id = @to_translation_id,
    from_segment = COALESCE(from_segment, @from_segment::text)
WHERE translation_id = @from_translation_id;

-- name: MoveTranslationHistory :exec
-- Internal: a join moves the history of the row it removes to the row it
-- keeps. Each event keeps the number of the row it happened on, the
-- translation it was written for and that row's source words and
-- characters, so metrics of past periods do not change; values an earlier
-- split or join stored stay.
UPDATE translation_history th
SET translation_id = @to_translation_id,
    details = jsonb_build_object('from_segment', @from_segment::text, 'translation_id', th.translation_id,
                                 'word_count', s.word_count, 'char_count', length(s.source_text))
              || COALESCE(th.details, '{}'::jsonb)
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE t.id = @from_translation_id AND th.translation_id = t.id;

-- name: SnapshotHistoryCounts :exec
-- Internal: before a split or join moves a translation to another segment,
-- its history keeps the source words and characters of the segment it was
-- written for, so metrics of past periods do not change; values an earlier
-- split or join stored stay.
UPDATE translation_history th
SET details = jsonb_build_object('word_count', s.word_count, 'char_count', length(s.source_text))
              || COALESCE(th.details, '{}'::jsonb)
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE t.id = @translation_id AND th.translation_id = t.id;

-- name: ForgetStepRoundTexts :exec
-- Internal: a split or join changes the source under these translations, so
-- a step rollback must not restore a status a round ended with on the old
-- source: with no end text, the text never reads as unchanged.
UPDATE translation_step_statuses
SET end_text_md5 = NULL
WHERE translation_id = ANY(@translation_ids::bigint[]) AND end_text_md5 IS NOT NULL;

-- name: LockRowForJoin :one
-- Internal: a row a join removes, locked for the transaction. Matches nothing
-- when it changed since @expected_updated_at, is locked, is being
-- pre-translated or another user holds it.
SELECT id FROM translations
WHERE id = @id
  AND updated_at = @expected_updated_at
  AND NOT locked
  AND in_flight_phase IS NULL
  AND NOT translation_held_by_other(translations.id, @actor_id)
FOR UPDATE;

-- name: CountJobRowsInside :one
-- Tenant-scoped: how many of the job's rows lie strictly between
-- @from_position and @to_position, counted up to @row_cap: a join only needs
-- to know whether that is more than the rows it names.
SELECT count(*)::int FROM (
  SELECT 1
  FROM translations t
  JOIN segments s ON s.id = t.segment_id
  JOIN files f ON f.id = s.file_id
  JOIN projects p ON p.id = f.project_id
  WHERE s.file_id = @file_id AND t.target_language = @target_language
    AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
    AND s.position > @from_position::int AND s.position < @to_position::int
  LIMIT @row_cap::int
) inside;
