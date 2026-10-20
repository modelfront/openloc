-- queries.sql — saved analyses (migration 042). Two tables: `analyses`
-- (the entity) and `job_analysis`
-- (per-(analysis, job, target_language) breakdown with 48 metric columns).
--
-- Reads scope by tenant_id. Writes from the API handler are gated by the
-- pretranslate-completion rule (enforced in the handler, not here).


-- name: InsertAnalysis :one
-- Creates a pending analysis row; the worker picks it up via the
-- analyses_status_idx (partial index on pending/running). The job_ids
-- array is application-validated to belong to (project_id, tenant_id)
-- before this insert runs (no FK enforces it).
INSERT INTO analyses (
    tenant_id,
    project_id,
    name,
    source_language,
    job_ids,
    created_by,
    settings_snapshot
) VALUES (
    @tenant_id,
    @project_id,
    @name,
    @source_language,
    @job_ids,
    sqlc.narg('created_by')::bigint,
    @settings_snapshot
)
RETURNING *;


-- name: EnsurePendingAutoAnalysis :one
-- Coalescing insert for auto-analyze-on-upload. Ensures AT MOST ONE
-- pending auto analysis per project: the partial unique index
-- uq_pending_auto_analysis makes a second file in the same upload (or a burst
-- of uploads) hit ON CONFLICT DO NOTHING and return no row. job_ids stays empty
-- — the worker resolves the project's job set at run time, so files that join
-- the batch late are still covered.
INSERT INTO analyses (
    tenant_id,
    project_id,
    name,
    source_language,
    job_ids,
    settings_snapshot,
    is_auto
) VALUES (
    @tenant_id,
    @project_id,
    @name,
    @source_language,
    '{}',
    '{}'::jsonb,
    true
)
ON CONFLICT (project_id) WHERE is_auto AND status = 'pending' AND deleted_at IS NULL
DO NOTHING
RETURNING *;


-- name: GetPendingAutoAnalysis :one
-- The project's single pending auto analysis, if any. Used by the promotion
-- step to decide whether there's an upload waiting to be analyzed.
SELECT *
FROM analyses
WHERE project_id = @project_id
  AND is_auto
  AND status = 'pending'
  AND deleted_at IS NULL
LIMIT 1;


-- name: ListProjectJobsForAutoAnalysis :many
-- Every job in the project with the status fields the promotion gate needs
-- (mirrors the manual gate's readiness check). Internal: tenant scoping is done
-- by the caller before this runs.
SELECT j.id, j.status, j.current_step
FROM jobs j
JOIN files f ON f.id = j.file_id
WHERE f.project_id = @project_id;


-- name: ListAnalysesByProject :many
-- Project Analyses tab. Tenant-scoped. Most recent first. No breakdown rows
-- returned here — the list view shows summary (name, status, created_at,
-- total words derived from a separate aggregate query when needed).
SELECT *
FROM analyses
WHERE project_id = @project_id
  AND tenant_id  = @tenant_id
  AND deleted_at IS NULL
ORDER BY created_at DESC;


-- name: GetAnalysis :one
-- Tenant-scoped detail fetch. Pair with ListJobAnalysisByAnalysis for the
-- breakdown rows.
SELECT *
FROM analyses a
WHERE a.id        = @id
  AND a.tenant_id = @tenant_id
  AND a.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM projects p WHERE p.id = a.project_id AND p.deleted_at IS NULL);


-- name: SetAnalysisJobIDs :exec
-- Persist the job set resolved at run time onto a coalesced auto analysis.
-- is_auto rows are created with job_ids='{}' and the worker resolves
-- the project's jobs when it runs; writing them back keeps the row's stored
-- job_ids (and the UI's "jobs" count) accurate.
UPDATE analyses
SET job_ids = @job_ids
WHERE id = @id;


-- name: UpdateAnalysisRunning :execrows
-- Worker claim: pending → running. Returns 0 when the row is not pending, i.e.
-- another worker holds or has finished it; the caller must then stop. Run in
-- the worker's transaction, the row lock makes concurrent claims wait. A
-- deleted analysis is never claimed.
UPDATE analyses
SET status = 'running'
WHERE id     = @id
  AND status = 'pending'
  AND deleted_at IS NULL;


-- name: UpdateAnalysisCompleted :exec
-- Worker transition on success. completed_at must be set (CHECK constraint
-- on the table enforces this). Idempotent via the status guard.
UPDATE analyses
SET status       = 'completed',
    completed_at = NOW()
WHERE id     = @id
  AND status = 'running';


-- name: UpdateAnalysisFailed :exec
-- Worker transition on fatal error. error_message must be non-empty (CHECK
-- enforces). Idempotent via the status guard (pending OR running can fail
-- — pending fails if the worker rejects the input before flipping to
-- running, running fails mid-compute).
UPDATE analyses
SET status        = 'failed',
    error_message = @error_message,
    completed_at  = NOW()
WHERE id     = @id
  AND status IN ('pending', 'running');


-- name: DeleteAnalysis :execrows
-- Soft delete. The job_analysis breakdown rows stay with
-- the tombstoned analysis. Returns 0 when there is no live analysis to delete.
UPDATE analyses
SET deleted_at = NOW()
WHERE id        = @id
  AND tenant_id = @tenant_id
  AND deleted_at IS NULL;


-- name: InsertJobAnalysis :exec
-- Worker writes one breakdown row per (analysis_id, job_id, target_language).
-- 48 metric columns — verbose but flat. Auto-approved values are subsets of
-- the band totals; CHECK constraints on the table fail loud if the worker
-- ever emits a row that violates the invariant.
INSERT INTO job_analysis (
    analysis_id,
    job_id,
    target_language,
    job_name_snapshot,

    tm_101_segments,                       tm_101_words,                       tm_101_characters,                       tm_101_weighted_words,
    tm_101_segments_auto_approved,         tm_101_words_auto_approved,         tm_101_characters_auto_approved,         tm_101_weighted_words_auto_approved,

    tm_100_segments,                       tm_100_words,                       tm_100_characters,                       tm_100_weighted_words,
    tm_100_segments_auto_approved,         tm_100_words_auto_approved,         tm_100_characters_auto_approved,         tm_100_weighted_words_auto_approved,

    tm_85_99_segments,                     tm_85_99_words,                     tm_85_99_characters,                     tm_85_99_weighted_words,
    tm_85_99_segments_auto_approved,       tm_85_99_words_auto_approved,       tm_85_99_characters_auto_approved,       tm_85_99_weighted_words_auto_approved,

    tm_65_84_segments,                     tm_65_84_words,                     tm_65_84_characters,                     tm_65_84_weighted_words,
    tm_65_84_segments_auto_approved,       tm_65_84_words_auto_approved,       tm_65_84_characters_auto_approved,       tm_65_84_weighted_words_auto_approved,

    new_segments,                          new_words,                          new_characters,                          new_weighted_words,
    new_segments_auto_approved,            new_words_auto_approved,            new_characters_auto_approved,            new_weighted_words_auto_approved,

    repetitions_segments,                  repetitions_words,                  repetitions_characters,                  repetitions_weighted_words,
    repetitions_segments_auto_approved,    repetitions_words_auto_approved,    repetitions_characters_auto_approved,    repetitions_weighted_words_auto_approved
) VALUES (
    @analysis_id,
    sqlc.narg('job_id')::bigint,
    @target_language,
    @job_name_snapshot,

    @tm_101_segments,                      @tm_101_words,                      @tm_101_characters,                      @tm_101_weighted_words,
    @tm_101_segments_auto_approved,        @tm_101_words_auto_approved,        @tm_101_characters_auto_approved,        @tm_101_weighted_words_auto_approved,

    @tm_100_segments,                      @tm_100_words,                      @tm_100_characters,                      @tm_100_weighted_words,
    @tm_100_segments_auto_approved,        @tm_100_words_auto_approved,        @tm_100_characters_auto_approved,        @tm_100_weighted_words_auto_approved,

    @tm_85_99_segments,                    @tm_85_99_words,                    @tm_85_99_characters,                    @tm_85_99_weighted_words,
    @tm_85_99_segments_auto_approved,      @tm_85_99_words_auto_approved,      @tm_85_99_characters_auto_approved,      @tm_85_99_weighted_words_auto_approved,

    @tm_65_84_segments,                    @tm_65_84_words,                    @tm_65_84_characters,                    @tm_65_84_weighted_words,
    @tm_65_84_segments_auto_approved,      @tm_65_84_words_auto_approved,      @tm_65_84_characters_auto_approved,      @tm_65_84_weighted_words_auto_approved,

    @new_segments,                         @new_words,                         @new_characters,                         @new_weighted_words,
    @new_segments_auto_approved,           @new_words_auto_approved,           @new_characters_auto_approved,           @new_weighted_words_auto_approved,

    @repetitions_segments,                 @repetitions_words,                 @repetitions_characters,                 @repetitions_weighted_words,
    @repetitions_segments_auto_approved,   @repetitions_words_auto_approved,   @repetitions_characters_auto_approved,   @repetitions_weighted_words_auto_approved
);


-- name: ListJobAnalysisByAnalysis :many
-- All breakdown rows for an analysis, deterministic order so the detail
-- view renders the same way every time. job_id NULLs (deleted jobs) sort
-- last via NULLS LAST.
SELECT *
FROM job_analysis
WHERE analysis_id = @analysis_id
ORDER BY target_language ASC, job_id ASC NULLS LAST;


-- name: GetSavedAnalysisBands :many
-- Worker query: aggregates the per-(band, auto_approved) counts for ONE
-- (job, target_language) by reading stored `translations.tm_match_percent`
-- + `translations.status`. The worker pivots these rows into the wide
-- job_analysis row (5 bands × 2 auto-approved states = up to 10 rows per
-- call; the worker fills any unrepresented bucket with zeros).
--
-- Bands:
--   tm_101    — t.origin='tm' AND tm_match_percent=101 (101% in-context exact,
--               written when source + both document neighbors match)
--   tm_100    — t.origin='tm' AND tm_match_percent=100
--   tm_85_99  — t.origin='tm' AND tm_match_percent BETWEEN 85 AND 99
--   tm_65_84  — t.origin='tm' AND tm_match_percent BETWEEN 65 AND 84
--   new       — anything else (MT-filled, no-match, below-65 fuzzy, OR no
--               translation row at all via LEFT JOIN)
--
-- Repetitions are NOT computed here (behavior deferred; see planning doc).
--
-- is_auto_approved is FALSE for segments with no translation row (LEFT JOIN
-- yields NULL status) and for any status other than 'auto_approved' —
-- including 'approved' (human-confirmed) and 'needs_review'.
SELECT
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent = 101 THEN 'tm_101'
        WHEN t.origin = 'tm' AND t.tm_match_percent = 100 THEN 'tm_100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN 'tm_85_99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 65  THEN 'tm_65_84'
        ELSE 'new'
    END AS band,
    COALESCE(t.status = 'auto_approved', FALSE)::bool AS is_auto_approved,
    COUNT(*)::int AS segments,
    COALESCE(SUM(s.word_count), 0)::int AS words,
    COALESCE(SUM(length(s.source_text)), 0)::int AS characters
FROM segments s
JOIN jobs j ON j.file_id = s.file_id
LEFT JOIN translations t
    ON t.segment_id = s.id
   AND t.target_language = j.target_language
WHERE j.id = @job_id
GROUP BY band, is_auto_approved;


-- name: GetSavedAnalysisRepetitionBands :many
-- Batch worker query for the saved analysis: aggregates the per-(band,
-- auto_approved, is_repetition) counts for EVERY job in an analysis' job set in
-- one round-trip, and flags project-wide repetitions.
--
-- Repetitions: across the whole job set, segments sharing the same
-- (target_language, source_text) after the FIRST occurrence are counted in the
-- `repetitions` bucket INSTEAD of their leverage band (subtracted from it — the
-- worker routes is_repetition=true rows to the repetitions columns and does NOT
-- also count them in tm_101/…/new). The window partitions across ALL jobs in the
-- set, so repetitions are project-wide (or selection-wide for a manual analysis),
-- not per-file. rn=1 keeps its real band; rn>1 is a repetition. Ordering by
-- (file_id, position) makes "first occurrence" deterministic.
--
-- The band + auto_approved CASE expressions are identical to the per-job
-- GetSavedAnalysisBands, so non-repeated results match the single-job query.
-- job_id = j.id (a job is a (file, target_language) pair; the same file across
-- two target languages yields two jobs and two partitions).
WITH scoped AS (
    SELECT
        j.id AS job_id,
        CASE
            WHEN t.origin = 'tm' AND t.tm_match_percent = 101 THEN 'tm_101'
            WHEN t.origin = 'tm' AND t.tm_match_percent = 100 THEN 'tm_100'
            WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN 'tm_85_99'
            WHEN t.origin = 'tm' AND t.tm_match_percent >= 65  THEN 'tm_65_84'
            ELSE 'new'
        END AS band,
        COALESCE(t.status = 'auto_approved', FALSE)::bool AS is_auto_approved,
        (ROW_NUMBER() OVER (
            PARTITION BY j.target_language, s.source_text
            ORDER BY j.file_id, s.position
        ) > 1) AS is_repetition,
        s.word_count AS words,
        length(s.source_text) AS characters
    FROM segments s
    JOIN jobs j ON j.file_id = s.file_id
    LEFT JOIN translations t
        ON t.segment_id = s.id
       AND t.target_language = j.target_language
    WHERE j.id = ANY(@job_ids::bigint[])
)
SELECT
    job_id,
    band,
    is_auto_approved,
    is_repetition,
    COUNT(*)::int AS segments,
    COALESCE(SUM(words), 0)::int AS words,
    COALESCE(SUM(characters), 0)::int AS characters
FROM scoped
GROUP BY job_id, band, is_auto_approved, is_repetition;


-- name: ListJobsByIDsForProject :many
-- Handler validation (POST /projects/{id}/analyses): for each job_id in the
-- request body, confirm it belongs to the given project and report its
-- current pipeline status (so the gate can reject "not yet pretranslated"
-- selections). Cross-project ids simply don't appear in the result; the
-- handler compares result-length to request-length to detect that case.
SELECT j.id, j.status, j.current_step, j.target_language, f.name AS file_name
FROM jobs j
JOIN files f ON f.id = j.file_id
WHERE j.id = ANY(@job_ids::bigint[])
  AND f.project_id = @project_id;


-- name: GetJobLeverageNeedsReview :many
-- Per-band needs-review count for the CAT-editor status bar (planning
-- doc §"Live job-level view"). Returns one row per band whose segments
-- contain at least one status='needs_review' translation — bands with
-- no needs-review entries simply don't appear, and the handler defaults
-- them to zero.
--
-- Same band CASE expression as GetSavedAnalysisBands so the two queries
-- bucket identically. The inner WHERE filters to needs-review rows
-- BEFORE the aggregation so the SUMs only count those segments.
SELECT
    CASE
        WHEN t.origin = 'tm' AND t.tm_match_percent = 101 THEN 'tm_101'
        WHEN t.origin = 'tm' AND t.tm_match_percent = 100 THEN 'tm_100'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 85  THEN 'tm_85_99'
        WHEN t.origin = 'tm' AND t.tm_match_percent >= 65  THEN 'tm_65_84'
        ELSE 'new'
    END AS band,
    COUNT(*)::int AS segments,
    COALESCE(SUM(s.word_count), 0)::int AS words,
    COALESCE(SUM(length(s.source_text)), 0)::int AS characters
FROM segments s
JOIN jobs j ON j.file_id = s.file_id
JOIN translations t
    ON t.segment_id = s.id
   AND t.target_language = j.target_language
WHERE j.id = @job_id
  AND t.status = 'needs_review'
GROUP BY band;


-- name: GetJobForAnalysis :one
-- Worker helper: pulls job context the worker needs per job — file name
-- (for job_name_snapshot), target_language, tenant_id (for cross-tenant
-- defense). Joins through files+projects so a single round-trip carries
-- everything; the worker doesn't have to make 3 lookups per job.
SELECT
    j.id            AS job_id,
    j.target_language,
    f.name          AS file_name,
    p.tenant_id     AS tenant_id,
    p.id            AS project_id
FROM jobs j
JOIN files f    ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = @job_id AND p.deleted_at IS NULL;
