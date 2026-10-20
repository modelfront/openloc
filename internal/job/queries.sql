-- name: CreateJob :one
INSERT INTO jobs (file_id, target_language, word_count)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetJob :one
-- Internal: used by pipeline. For user-facing handlers, use GetJobTenant.
SELECT * FROM jobs WHERE id = $1;

-- name: GetJobTenant :one
-- Tenant-scoped: returns job only if it belongs to the caller's tenant.
SELECT j.* FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: GetJobOwnershipContext :one
-- Tenant-scoped: verifies the job belongs to the caller's tenant AND returns the
-- surrounding entity IDs. Used by auth.RequireJobOwnership so a log line or an
-- error notice for /api/jobs/{jobID}/* can name the project and file, not just
-- the job.
--
-- Deliberately a separate query rather than widening GetJobTenant: that one
-- returns db.Job to 18 call sites, and adding a column changes its generated row
-- type for all of them. The joins here are identical to GetJobTenant's, so the
-- ownership check costs exactly what it did before — no extra round trip.
SELECT j.id, j.file_id, f.project_id, j.target_language FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: GetJobByFileAndLang :one
SELECT * FROM jobs WHERE file_id = $1 AND target_language = $2;

-- name: ListJobsByProject :many
SELECT j.id, j.file_id, j.target_language, j.word_count, j.status, j.current_step,
       j.created_at, j.updated_at,
       j.error_message, j.translated_count, j.total_count, j.stop_requested_at, j.mt_skip_reason,
       f.name AS file_name, f.format AS file_format, f.inline_html AS file_inline_html, f.segment_count,
       f.created_at AS file_created_at
FROM jobs j
JOIN files f ON f.id = j.file_id
WHERE f.project_id = $1
ORDER BY f.created_at DESC, j.target_language;

-- name: ListJobsByProjectPage :many
-- Internal: no tenant filter. job.Service.ListPageByProject checks the
-- project with the tenant-scoped GetProject before calling it. One keyset page
-- of a project's jobs, oldest first; same columns as ListJobsByProject.
SELECT j.id, j.file_id, j.target_language, j.word_count, j.status, j.current_step,
       j.created_at, j.updated_at,
       j.error_message, j.translated_count, j.total_count, j.stop_requested_at, j.mt_skip_reason,
       f.name AS file_name, f.format AS file_format, f.inline_html AS file_inline_html, f.segment_count,
       f.created_at AS file_created_at
FROM jobs j
JOIN files f ON f.id = j.file_id
WHERE f.project_id = @project_id AND j.id > @cursor
ORDER BY j.id
LIMIT @page_limit;

-- name: ListJobsByFile :many
SELECT * FROM jobs WHERE file_id = $1 ORDER BY target_language;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;

-- name: DeleteJobTenant :exec
-- Tenant-scoped delete: only if job belongs to caller's tenant.
DELETE FROM jobs
WHERE jobs.id = $1 AND file_id IN (
    SELECT f.id FROM files f JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $2 AND p.deleted_at IS NULL
);

-- name: DeleteJobTranslations :exec
-- Deletes all translations for a specific file + target language.
-- Needed because translations FK to segments (not jobs), so deleting
-- a job row alone would leave translations orphaned.
DELETE FROM translations
WHERE segment_id IN (SELECT id FROM segments WHERE file_id = $1)
  AND target_language = $2;

-- name: UpdateJobStatus :exec
-- Clears error_message: every caller passes a non-failed status (queued /
-- in_progress / completed), so reaching a new state means any prior failure is
-- stale. The failure path writes error_message via UpdateJobError instead.
-- Also clears stop_requested_at: every caller is a start or end transition, so
-- a pending stop request is stale there too. The worker's start transition is
-- StartJobRun instead, which must NOT clear it (a stop requested while the
-- task sat in the queue has to be honoured at pickup).
UPDATE jobs SET status = $2, current_step = $3, error_message = NULL, stop_requested_at = NULL, updated_at = now() WHERE id = $1;

-- name: UpdateJobError :exec
-- A failed job must not keep reading "Stopping…", so the flag is cleared here too.
UPDATE jobs SET status = 'failed', error_message = $2, stop_requested_at = NULL, updated_at = now() WHERE id = $1;

-- name: DismissJobFailure :one
-- Accepts a failed job's error when a person completes or sends back its task:
-- the job goes idle and the error is cleared, so it does not reappear on the
-- task that becomes current next. Returns the dismissed message; pgx.ErrNoRows
-- means the job was not failed. Tenant scoping is the caller's.
WITH old AS (
    SELECT j.id, j.error_message FROM jobs j WHERE j.id = @id AND j.status = 'failed' FOR UPDATE
)
UPDATE jobs SET status = 'in_progress', current_step = @current_step, error_message = NULL,
       stop_requested_at = NULL, updated_at = now()
FROM old
WHERE jobs.id = old.id
RETURNING old.error_message;

-- name: ClaimFailedJobForRerun :execrows
-- Moves a failed job to queued for a Re-run of the automation that failed.
-- 0 rows = the job is not failed, or a concurrent Re-run claimed it first
-- → 409. Tenant scoping happens in resolveJob before this by-ID write.
UPDATE jobs SET status = 'queued', current_step = @current_step, error_message = NULL,
       stop_requested_at = NULL, updated_at = now()
WHERE id = @id AND status = 'failed';

-- name: SaveJobRunOutcome :exec
-- What a retried run's earlier attempts filled. Scoped by tenant through the
-- job's project: a job id that is not the tenant's writes nothing.
INSERT INTO job_run_outcomes (job_id, outcome)
SELECT j.id, sqlc.arg(outcome)::jsonb
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = sqlc.arg(job_id) AND p.tenant_id = sqlc.arg(tenant_id) AND p.deleted_at IS NULL
ON CONFLICT (job_id) DO UPDATE SET outcome = EXCLUDED.outcome, updated_at = now();

-- name: GetJobRunOutcome :one
-- Scoped by tenant as SaveJobRunOutcome.
SELECT o.outcome
FROM job_run_outcomes o
JOIN jobs j ON j.id = o.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE o.job_id = sqlc.arg(job_id) AND p.tenant_id = sqlc.arg(tenant_id) AND p.deleted_at IS NULL;

-- name: DeleteJobRunOutcome :exec
-- Scoped by tenant as SaveJobRunOutcome. A scratch row with no history of its
-- own, so it is deleted, not soft-deleted.
DELETE FROM job_run_outcomes o
USING jobs j, files f, projects p
WHERE o.job_id = sqlc.arg(job_id) AND j.id = o.job_id AND f.id = j.file_id
  AND p.id = f.project_id AND p.tenant_id = sqlc.arg(tenant_id) AND p.deleted_at IS NULL;

-- name: MarkJobRetrying :exec
-- A worker attempt failed and asynq will try again. The job reads as running
-- (queued + a retry label) instead of failed, so nobody re-runs or completes
-- the task while the retry is pending; only the last attempt writes
-- UpdateJobError. stop_requested_at is kept: the next attempt's StartJobRun
-- honours a stop the user asked for.
UPDATE jobs SET status = 'queued', current_step = $2, error_message = $3, updated_at = now() WHERE id = $1;

-- name: ClaimJobPipeline :one
-- Atomic "not already running" guard + in_progress transition for the
-- synchronous phase handlers (mt-apply / tm-apply / ai-step). No row means
-- another run holds the job → 409. The running-state predicate is the SQL
-- twin of pipeline.IsPipelineRunning (translate.go) — keep the two in sync.
-- Clears error_message like UpdateJobStatus does (a new run makes any prior
-- failure stale). Tenant-scoped: a job outside the tenant claims nothing.
--
-- Returns the status, step, error_message and updated_at the claim replaced,
-- read under the row lock: a pre-flight rejection puts back exactly these,
-- not the job the handler loaded before the claim, which a run finishing in
-- between has since moved on.
--
-- Staleness escape: a row untouched for 30 minutes is reclaimable — a pod
-- hard-killed mid-run leaves the job in_progress forever, and without the
-- escape the claim would 409 it permanently. Safe because every LIVE run bumps
-- updated_at at least once per 30-segment chunk (the sync handlers' progress
-- writer and the worker's makeProgressFunc), far inside 30 minutes.
WITH prev AS (
    SELECT j.id, j.status, j.current_step, j.error_message, j.updated_at FROM jobs j
    JOIN files f ON f.id = j.file_id
    JOIN projects p ON p.id = f.project_id
    WHERE j.id = sqlc.arg(id) AND p.tenant_id = sqlc.arg(tenant_id) AND p.deleted_at IS NULL
    FOR UPDATE OF j
)
UPDATE jobs SET status = 'in_progress', current_step = sqlc.arg(current_step), error_message = NULL, stop_requested_at = NULL, updated_at = now()
FROM prev
WHERE jobs.id = prev.id
  AND (NOT (prev.status IN ('queued', 'in_progress')
            AND lower(coalesce(prev.current_step, '')) NOT LIKE '%complete')
       OR prev.updated_at < now() - interval '30 minutes')
RETURNING prev.status, prev.current_step, prev.error_message, prev.updated_at;

-- name: RequestJobStop :one
-- Cooperative stop: flags a RUNNING job (same predicate as ClaimJobPipeline,
-- the SQL twin of pipeline.IsPipelineRunning) that has no pending request, and
-- reports whether the run it flagged was already stale — a row untouched for
-- 30 minutes belongs to a pod that died, so nothing will ever consume the flag
-- and the caller closes the job out itself.
--
-- pgx.ErrNoRows = nothing running, or already stopping → 409.
--
-- The staleness verdict must come from the row version the flag lands on:
-- decided from a separately-read snapshot, a ClaimJobPipeline whose own
-- 30-minute escape reclaims the same stale row could start a live run in
-- between, and the caller would then close out a job that is genuinely
-- translating. FOR UPDATE serialises this against that claim. Bumping
-- updated_at also takes the row out of reach of the escape, so no claim can
-- start between this statement and the caller's close-out.
--
-- Tenant scoping happens in the handler via GetJobTenant before this by-ID write.
WITH target AS (
    SELECT j.id AS job_id, (j.updated_at < now() - interval '30 minutes')::boolean AS stale
    FROM jobs j
    WHERE j.id = $1
      AND j.status IN ('queued', 'in_progress')
      AND lower(coalesce(j.current_step, '')) NOT LIKE '%complete'
      AND j.stop_requested_at IS NULL
    FOR UPDATE
)
UPDATE jobs SET stop_requested_at = now(), updated_at = now()
FROM target
WHERE jobs.id = target.job_id
RETURNING target.stale;

-- name: GetJobRunState :one
-- Read by the progress writers at every chunk boundary. ErrNoRows = the job
-- was deleted mid-flight; stop_requested = a user asked the run to stop.
SELECT (stop_requested_at IS NOT NULL)::boolean AS stop_requested,
       translated_count, total_count, updated_at
FROM jobs WHERE id = $1;

-- name: StartJobRun :execrows
-- Worker-side start transition. Refuses (0 rows) when a stop was requested
-- while the task sat in the queue: the handler then marks the job stopped and
-- returns nil, so asynq does not retry it.
UPDATE jobs SET status = 'in_progress', current_step = $2, error_message = NULL, updated_at = now()
WHERE id = $1 AND stop_requested_at IS NULL;

-- name: MarkJobStopped :exec
-- Terminal, non-running state: 'stopped' is outside IsPipelineRunning's
-- (queued, in_progress), so Apply TM/MT can re-claim the job and resume —
-- the pipeline is idempotent at segment level.
UPDATE jobs SET status = 'stopped', current_step = $2, stop_requested_at = NULL,
       error_message = NULL, updated_at = now()
WHERE id = $1;

-- name: RestoreJobPipelineState :exec
-- Internal: caller must have claimed the job with the tenant-scoped
-- ClaimJobPipeline.
-- Undo an eager ClaimJobPipeline after a pre-flight 400 (no work actually
-- ran): put back what the claim replaced — status, step, error_message (the
-- claim nulled it — a previously-failed job must keep its explanation) and
-- updated_at, so a dead run's job stays reclaimable. Conditional on the
-- claim's own step so a concurrent writer (worker enqueue, feedback
-- completion) that moved the job since the claim is never clobbered.
UPDATE jobs SET status = $2, current_step = $3, error_message = $4, updated_at = sqlc.arg(updated_at)
WHERE id = $1 AND status = 'in_progress' AND current_step = sqlc.arg(claim_step);

-- name: UpdateJobProgress :exec
UPDATE jobs SET translated_count = $2, total_count = $3, current_step = $4, updated_at = now() WHERE id = $1;

-- name: SetJobAsynqTaskID :exec
UPDATE jobs SET asynq_task_id = $2 WHERE id = $1;

-- name: GetJobTranslationCounts :one
-- Counts from job_translation_stats, which the database keeps up to date on
-- every translation write. The status enum is needs_review/auto_approved/approved;
-- untranslated + pre_translated are derived sub-states of needs_review that
-- the analysis bar + pipeline-action gating rely on. Partition is exhaustive: untranslated, pre_translated,
-- needs_review(display), auto_approved, approved sum to total.
--   untranslated   = no target text (text_state 0)
--   pre_translated = needs_review with text nobody edited in the current step
--                    and no AI verdict from the current step round
--   needs_review   = needs_review with text edited in the current step, or
--                    given an AI verdict in its current round
-- The clear counts exclude locked and already-empty rows and mirror the
-- ClearTranslationsByJob predicate, so the Clear-translations dialog shows
-- the real number.
SELECT
    COALESCE(SUM(st.segments), 0)::int AS total,
    COALESCE(SUM(st.segments) FILTER (WHERE st.text_state = 0), 0)::int AS untranslated,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state > 0
        AND NOT st.step_edited AND NOT st.predict_this_round), 0)::int AS pre_translated,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state > 0
        AND (st.step_edited OR st.predict_this_round)), 0)::int AS needs_review,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'auto_approved'), 0)::int AS auto_approved,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'approved'), 0)::int AS approved,
    COALESCE(SUM(st.segments) FILTER (WHERE NOT st.locked AND st.text_state > 0
        AND st.status NOT IN ('approved', 'auto_approved')), 0)::int AS clearable_non_confirmed,
    COALESCE(SUM(st.segments) FILTER (WHERE NOT st.locked AND st.text_state > 0), 0)::int AS clearable_all
FROM job_translation_stats st
WHERE st.file_id = $1 AND st.target_language = $2;

-- name: GetTranslationCountsByProject :many
-- Batch version of GetJobTranslationCounts: counts for the jobs of a project
-- in one query, grouped by (file_id, target_language). job_ids NULL counts
-- every job; otherwise only those jobs. A job with no translations has no
-- row. Bucket definitions: see GetJobTranslationCounts.
SELECT j.file_id, j.target_language,
    SUM(st.segments)::int AS total,
    COALESCE(SUM(st.segments) FILTER (WHERE st.text_state = 0), 0)::int AS untranslated,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state > 0
        AND NOT st.step_edited AND NOT st.predict_this_round), 0)::int AS pre_translated,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state > 0
        AND (st.step_edited OR st.predict_this_round)), 0)::int AS needs_review,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'auto_approved'), 0)::int AS auto_approved,
    COALESCE(SUM(st.segments) FILTER (WHERE st.status = 'approved'), 0)::int AS approved,
    COALESCE(SUM(st.segments) FILTER (WHERE NOT st.locked AND st.text_state > 0
        AND st.status NOT IN ('approved', 'auto_approved')), 0)::int AS clearable_non_confirmed,
    COALESCE(SUM(st.segments) FILTER (WHERE NOT st.locked AND st.text_state > 0), 0)::int AS clearable_all
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN job_translation_stats st ON st.file_id = j.file_id AND st.target_language = j.target_language
WHERE f.project_id = @project_id
  AND (sqlc.narg('job_ids')::bigint[] IS NULL OR j.id = ANY(sqlc.narg('job_ids')::bigint[]))
GROUP BY j.file_id, j.target_language
HAVING SUM(st.segments) > 0;

-- name: ListTasksFiltered :many
-- Tasks page: jobs whose current active step is assigned to someone, scoped to
-- the caller's tenant. assigned_to NULL = every assignee (owner/admin view);
-- otherwise only that user's tasks. The handler picks assigned_to from the
-- caller's role, never from the client. Every other filter is optional.
--
-- deadline buckets are measured against now() and line up with the deadline
-- chip bands: overdue, 24h (due within a day), week (due within 7 days), none.
--
-- Sorted by deadline (no deadline last), then newest job first. Keyset paging
-- on that same pair: after=false is the first page, otherwise rows strictly
-- after (cursor_deadline, cursor_id). A missing deadline sorts as 'infinity'.
-- Caller passes limit+1 to detect has_more.
SELECT j.id, j.target_language, j.status, j.current_step, jsa.deadline, j.word_count,
       j.translated_count, j.total_count,
       f.id AS file_id, f.name AS file_name,
       p.id AS project_id, p.name AS project_name, p.source_language,
       cv.value AS customer_name,
       ws.name AS step_name,
       COALESCE(NULLIF(u.name, ''), u.email, '')::text AS assignee_name
FROM jobs j
JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
-- Customer is a built-in label, so the row's customer is
-- whichever value the project picked.
LEFT JOIN label_definitions cd ON cd.tenant_id = p.tenant_id
                              AND cd.system_key = 'customer' AND cd.deleted_at IS NULL
LEFT JOIN project_labels cpl ON cpl.project_id = p.id AND cpl.label_definition_id = cd.id
LEFT JOIN label_values cv ON cv.id = cpl.label_value_id AND cv.deleted_at IS NULL
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND jsa.status = 'in_progress'
  AND jsa.assigned_to IS NOT NULL
  AND (sqlc.narg('assigned_to')::bigint IS NULL OR jsa.assigned_to = sqlc.narg('assigned_to')::bigint)
  AND (sqlc.narg('q')::text IS NULL
       OR f.name ILIKE '%' || sqlc.narg('q')::text || '%'
       OR p.name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (sqlc.narg('step_name')::text IS NULL OR ws.name = sqlc.narg('step_name')::text)
  AND (sqlc.narg('deadline')::text IS NULL
       OR (sqlc.narg('deadline')::text = 'overdue' AND jsa.deadline < now())
       OR (sqlc.narg('deadline')::text = '24h' AND jsa.deadline >= now() AND jsa.deadline < now() + interval '24 hours')
       OR (sqlc.narg('deadline')::text = 'week' AND jsa.deadline >= now() AND jsa.deadline < now() + interval '7 days')
       OR (sqlc.narg('deadline')::text = 'none' AND jsa.deadline IS NULL))
  AND (NOT @after::boolean
       OR COALESCE(jsa.deadline, 'infinity'::timestamptz) > @cursor_deadline::timestamptz
       OR (COALESCE(jsa.deadline, 'infinity'::timestamptz) = @cursor_deadline::timestamptz
           AND j.id < @cursor_id::bigint))
ORDER BY COALESCE(jsa.deadline, 'infinity'::timestamptz) ASC, j.id DESC
LIMIT @lim;

-- name: ListTaskStepNames :many
-- Distinct step names across the Tasks page's whole scope (every page, not only
-- the loaded rows) — the option set for the page's Step filter. Same scope rule
-- as ListTasksFiltered: assigned_to NULL = every assignee.
SELECT DISTINCT ws.name
FROM jobs j
JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND jsa.status = 'in_progress'
  AND jsa.assigned_to IS NOT NULL
  AND (sqlc.narg('assigned_to')::bigint IS NULL OR jsa.assigned_to = sqlc.narg('assigned_to')::bigint)
ORDER BY ws.name;

-- name: IsUserAssignedToJob :one
-- Vendor access gate: true if the user is assigned to ANY step of the job.
SELECT EXISTS (
    SELECT 1 FROM job_step_assignments
    WHERE job_id = @job_id AND assigned_to = @user_id
);

-- name: IsUserAssignedToFile :one
-- Vendor access gate for the file's language-neutral routes (source segments,
-- source download): true if the user is assigned to any step of any job of the
-- file. Translation routes use IsUserAssignedToFileLanguage.
SELECT EXISTS (
    SELECT 1
    FROM job_step_assignments jsa
    JOIN jobs j ON j.id = jsa.job_id
    WHERE j.file_id = @file_id AND jsa.assigned_to = @user_id
);

-- name: IsUserAssignedToFileLanguage :one
-- Vendor access gate for one language of a file: true if the user is assigned
-- to a step of the file's job in that target language. Translations, their
-- export and their comments are per language, so a vendor holding the DE job
-- must not reach the FR rows of the same file.
SELECT EXISTS (
    SELECT 1
    FROM job_step_assignments jsa
    JOIN jobs j ON j.id = jsa.job_id
    WHERE j.file_id = @file_id AND j.target_language = @target_language
      AND jsa.assigned_to = @user_id
);

-- name: IsUserAssignedToTranslation :one
-- IsUserAssignedToFileLanguage keyed by a translation: the job of the
-- translation's own file and target language.
SELECT EXISTS (
    SELECT 1
    FROM translations t
    JOIN segments s ON s.id = t.segment_id
    JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
    JOIN job_step_assignments jsa ON jsa.job_id = j.id
    WHERE t.id = @translation_id AND jsa.assigned_to = @user_id
);

-- name: GetStoredMTForTranslation :one
-- Latest pipeline MT output for a translation (translation_history kind='mt'),
-- for the CAT sidebar's MT preview. Scoped to the job's file + target language
-- (the caller has already tenant-verified the job via GetJobTenant) so a
-- translation_id from another job or tenant resolves to no row instead of
-- leaking text. Rows with empty text are skipped — they carry nothing worth
-- showing, and the caller's live fallback handles them.
-- rich_text rides along so a tagged MT suggestion reaches the sidebar as chips
-- and inserts with its tags; the plain text alone would drop every tag from the
-- segment on click.
SELECT th.text, th.rich_text, th.details, th.mt_engine_id, th.ai_engine_id
FROM translation_history th
JOIN translations t ON t.id = th.translation_id
JOIN segments s ON s.id = t.segment_id
WHERE th.translation_id = @translation_id
  AND th.kind = 'mt'
  AND s.file_id = @file_id
  AND t.target_language = @target_language
  AND th.text IS NOT NULL AND th.text <> ''
ORDER BY th.id DESC
LIMIT 1;

-- name: GetSegmentRichTextForTranslation :one
-- Internal: the segment's source rich text for one translation, for the CAT
-- sidebar's live MT preview. Scoped to the job's file + target language (the
-- caller has already tenant-verified the job via GetJobTenant) so a
-- translation_id from another job or tenant resolves to no row.
SELECT s.source_rich_text
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE t.id = @translation_id
  AND s.file_id = @file_id
  AND t.target_language = @target_language;

-- name: SetJobMTSkipReason :exec
-- Internal: worker-side, after a pre-translation run. NULL clears it.
UPDATE jobs SET mt_skip_reason = sqlc.narg(reason), updated_at = now()
WHERE id = sqlc.arg(id);
