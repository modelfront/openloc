-- ============================================================
-- QA Profiles
-- ============================================================

-- QA profiles are soft-deleted: live reads filter
-- deleted_at IS NULL.

-- name: ListQAProfiles :many
SELECT * FROM qa_profiles
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY name;

-- name: GetQAProfile :one
SELECT * FROM qa_profiles
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateQAProfile :one
INSERT INTO qa_profiles (tenant_id, name, description)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateQAProfile :one
UPDATE qa_profiles
SET name = $2, description = $3, updated_at = now()
WHERE id = $1 AND tenant_id = $4 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteQAProfile :exec
UPDATE qa_profiles SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- ============================================================
-- QA Rules
-- ============================================================

-- name: ListQARules :many
SELECT * FROM qa_rules
WHERE profile_id = $1
ORDER BY check_type;

-- name: UpsertQARule :one
INSERT INTO qa_rules (profile_id, check_type, enabled, severity, config)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (profile_id, check_type)
DO UPDATE SET enabled = EXCLUDED.enabled, severity = EXCLUDED.severity, config = EXCLUDED.config
RETURNING *;

-- name: DeleteQARule :exec
DELETE FROM qa_rules WHERE id = $1;

-- ============================================================
-- QA Results
-- ============================================================

-- name: UpsertQAResult :exec
-- Internal: caller must have tenant-verified the translation (GetTranslationTenant / GetJobTenant).
-- A QA run upserts in place so the per-finding ignore state survives it. The
-- ignore is kept only while the finding is unchanged: a different message means
-- a different problem, which must show up as new.
INSERT INTO qa_results (translation_id, check_type, severity, message)
VALUES ($1, $2, $3, $4)
ON CONFLICT (translation_id, check_type)
DO UPDATE SET
    severity      = EXCLUDED.severity,
    message       = EXCLUDED.message,
    created_at    = now(),
    ignored       = qa_results.ignored AND qa_results.message = EXCLUDED.message,
    ignored_by    = CASE WHEN qa_results.message = EXCLUDED.message THEN qa_results.ignored_by END,
    ignored_at    = CASE WHEN qa_results.message = EXCLUDED.message THEN qa_results.ignored_at END;

-- name: PruneQAResultsForTranslation :exec
-- Internal: caller must have tenant-verified the translation (GetTranslationTenant / GetJobTenant).
-- Removes the rows for checks that did not fire in the run that just upserted
-- keep_check_types. Pass an empty array to clear every finding of the segment.
DELETE FROM qa_results
WHERE translation_id = @translation_id
  AND NOT (check_type = ANY(@keep_check_types::text[]));

-- name: DeleteQAResultsForJob :exec
-- Internal: caller must have tenant-verified the job (GetJobTenant).
-- Only for the "no enabled rules" path: with no rules there are no findings,
-- so there is no ignore state worth preserving.
DELETE FROM qa_results
WHERE translation_id IN (
    SELECT t.id FROM translations t
    JOIN segments s ON s.id = t.segment_id
    JOIN files f ON f.id = s.file_id
    WHERE f.id = @file_id AND t.target_language = @target_language
);

-- name: DeleteQAResultsForTranslations :exec
-- Internal: caller must have tenant-verified the translations (the clear
-- handlers verify the file / job / project first).
-- For writes that replace a target outside a QA run — Clear, and the TM/MT
-- pre-translate phases. Every finding was computed against text that is gone,
-- so the ignore state goes with it: an ignore says "this text is acceptable",
-- which claims nothing about whatever replaces it.
DELETE FROM qa_results
WHERE translation_id = ANY(@translation_ids::bigint[]);

-- name: SetQAResultIgnored :one
-- Tenant-scoped through the translation → project chain. Un-ignoring clears
-- by/at so a later ignore starts clean.
UPDATE qa_results qr
SET ignored       = @ignored::boolean,
    ignored_by    = CASE WHEN @ignored::boolean THEN @user_id::bigint ELSE NULL END,
    ignored_at    = CASE WHEN @ignored::boolean THEN now() ELSE NULL END
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE qr.translation_id = t.id
  AND qr.translation_id = @translation_id
  AND qr.check_type = @check_type
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
RETURNING qr.*;

-- name: ListQAResultsForTranslation :many
-- Internal: caller must have tenant-verified the translation (GetTranslationTenant).
SELECT qr.*, u.name AS ignored_by_name
FROM qa_results qr
LEFT JOIN users u ON u.id = qr.ignored_by
WHERE qr.translation_id = $1
ORDER BY qr.severity, qr.check_type;

-- name: ListQAResultsForJob :many
-- Internal: caller must have tenant-verified the job (GetJobTenant).
SELECT qr.*, t.segment_id, s.position AS segment_position, u.name AS ignored_by_name
FROM qa_results qr
JOIN translations t ON t.id = qr.translation_id
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
LEFT JOIN users u ON u.id = qr.ignored_by
WHERE f.id = @file_id AND t.target_language = @target_language
ORDER BY s.position, qr.severity, qr.check_type;

-- name: ListTranslationIDsWithQAResults :many
-- Internal: caller must have tenant-verified the job (GetJobTenant) and pass
-- ids of its rows. Lets a QA run skip the write for segments that have no
-- findings now and had none before — on a large file that is most segments.
SELECT DISTINCT qr.translation_id
FROM qa_results qr
WHERE qr.translation_id = ANY(@ids::bigint[]);

-- name: CountQAIssuesByJob :many
-- Internal: caller must have tenant-verified the job (GetJobTenant).
-- Ignored findings leave their severity bucket and land in 'ignored', so the
-- summary counts only what still needs attention.
SELECT (CASE WHEN qr.ignored THEN 'ignored' ELSE qr.severity END)::text AS bucket,
       count(*)::int AS count
FROM qa_results qr
JOIN translations t ON t.id = qr.translation_id
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
WHERE f.id = @file_id AND t.target_language = @target_language
GROUP BY 1;

-- ============================================================
-- Segments + translations for QA batch processing
-- (loads all translations for a job to run QA checks)
-- ============================================================

-- name: ListOtherTargetsForSource :many
-- For consistency check: find other translations of the same source text in this job.
-- ORDER BY is load-bearing: CheckConsistency quotes the first differing row, and
-- a finding's message is its identity for the per-finding ignore.
SELECT COALESCE(t2.active_text, '') AS target_text
FROM translations t2
JOIN segments s2 ON s2.id = t2.segment_id
JOIN files f2 ON f2.id = s2.file_id
WHERE f2.id = @file_id
  AND t2.target_language = @target_language
  AND md5(s2.source_text) = md5(@source_text::text)
  AND s2.source_text = @source_text
  AND t2.id != @exclude_translation_id
ORDER BY t2.id
LIMIT 10;

-- name: ListTargetsForSources :many
-- Internal: caller must have tenant-verified the job (GetJobTenant).
-- ListOtherTargetsForSource for a batch of a job run: for each of @sources
-- (with @hashes = md5 of each, for idx_segments_file_source_md5), the first 11
-- rows in t2.id order. The caller drops the segment's own row and keeps 10,
-- which is exactly the per-segment query's answer.
SELECT ranked.source_text, ranked.translation_id, ranked.target_text
FROM (
    SELECT s2.source_text, t2.id AS translation_id,
           COALESCE(t2.active_text, '') AS target_text,
           row_number() OVER (PARTITION BY s2.source_text ORDER BY t2.id) AS rn
    FROM segments s2
    JOIN translations t2 ON t2.segment_id = s2.id AND t2.target_language = @target_language
    WHERE s2.file_id = @file_id
      AND md5(s2.source_text) = ANY(@hashes::text[])
      AND s2.source_text = ANY(@sources::text[])
) ranked
WHERE ranked.rn <= 11
ORDER BY ranked.source_text, ranked.translation_id;

-- name: ListTranslationsForQAPage :many
-- Internal: caller must have tenant-verified the job (GetJobTenant).
-- One page of the job's rows in segment order. Positions can repeat within a
-- file, so the page key is (position, segment id).
SELECT t.id AS translation_id, s.id AS segment_id, s.position, s.source_text,
       COALESCE(t.active_text, '') AS target_text,
       COALESCE(s.source_rich_text, '') AS source_rich_text,
       COALESCE(t.active_rich_text, '') AS target_rich_text
FROM segments s
JOIN translations t ON t.segment_id = s.id AND t.target_language = @target_language
WHERE s.file_id = @file_id
  AND (s.position, s.id) > (@after_position::int, @after_segment_id::bigint)
ORDER BY s.position, s.id
LIMIT @page_size;

-- name: UpsertQAResults :exec
-- Internal: caller must have tenant-verified the translations (GetJobTenant).
-- UpsertQAResult for many rows at once; (translation_id, check_type) pairs
-- must be unique within one call.
INSERT INTO qa_results (translation_id, check_type, severity, message)
SELECT unnest(@translation_ids::bigint[]), unnest(@check_types::text[]), unnest(@severities::text[]), unnest(@messages::text[])
ON CONFLICT (translation_id, check_type)
DO UPDATE SET
    severity      = EXCLUDED.severity,
    message       = EXCLUDED.message,
    created_at    = now(),
    ignored       = qa_results.ignored AND qa_results.message = EXCLUDED.message,
    ignored_by    = CASE WHEN qa_results.message = EXCLUDED.message THEN qa_results.ignored_by END,
    ignored_at    = CASE WHEN qa_results.message = EXCLUDED.message THEN qa_results.ignored_at END;

-- name: PruneQAResultsForTranslations :exec
-- Internal: caller must have tenant-verified the translations (GetJobTenant).
-- PruneQAResultsForTranslation for many rows: removes each row's findings
-- whose check is not among its (keep_ids, keep_check_types) pairs.
DELETE FROM qa_results qr
WHERE qr.translation_id = ANY(@translation_ids::bigint[])
  AND NOT EXISTS (
      SELECT 1
      FROM (SELECT unnest(@keep_ids::bigint[]) AS id, unnest(@keep_check_types::text[]) AS check_type) k
      WHERE k.id = qr.translation_id AND k.check_type = qr.check_type
  );

-- name: CountQAProfileReferences :one
-- Count how many settings profiles reference this QA profile.
-- Used to warn before deletion.
SELECT count(*)::int AS count
FROM settings_profiles
WHERE qa_profile_id = $1
  AND settings_profile_live(settings_profiles.id);


-- name: ListIgnoredQAResultsForTranslations :many
-- Internal: caller must have tenant-verified the job (GetJobTenant) and pass
-- ids of its rows. The ignored findings of those rows, for the per-finding
-- ignore of a batch check.
SELECT translation_id, check_type, message
FROM qa_results
WHERE translation_id = ANY(@ids::bigint[]) AND ignored;
