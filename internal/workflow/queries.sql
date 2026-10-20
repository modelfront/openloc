-- name: GetWorkflowStep :one
SELECT * FROM workflow_steps WHERE id = $1;

-- ============================================================
-- Job Step Assignments
-- ============================================================

-- name: CreateJobStepAssignment :one
INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status, deadline)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListJobStepAssignments :many
SELECT jsa.*, ws.name AS step_name, ws.step_order, ws.role_required, ws.step_type, ws.ai_router_id,
       u.name AS assignee_name, u.email AS assignee_email
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE jsa.job_id = $1
ORDER BY ws.step_order;

-- name: GetCurrentStepForJob :one
SELECT jsa.*, ws.name AS step_name, ws.step_order, ws.role_required, ws.step_type,
       (ws.ai_router_id IS NOT NULL)::bool AS is_ai_step,
       u.name AS assignee_name, u.email AS assignee_email
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE jsa.id = @step_assignment_id;

-- name: GetActiveStepForJob :one
SELECT jsa.*, ws.name AS step_name, ws.step_order, ws.role_required, ws.step_type,
       u.name AS assignee_name, u.email AS assignee_email
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE jsa.job_id = @job_id AND jsa.status = 'in_progress'
LIMIT 1;

-- name: CompleteStepAssignment :exec
UPDATE job_step_assignments
SET status = 'completed', completed_at = now()
WHERE id = $1;

-- name: RejectStepAssignment :exec
-- The step that sends the job back drops its hand-off note: its next hand-off
-- writes a new one, and the note in between would be stale.
UPDATE job_step_assignments
SET status = 'rejected', completed_at = now(), rejection_reason = $2, handoff_note = NULL
WHERE id = $1;

-- name: ReopenStepAssignment :exec
UPDATE job_step_assignments
SET status = 'in_progress', started_at = now(), completed_at = NULL
WHERE id = $1;

-- name: ResetJobStepAssignments :exec
-- Reverts every step assignment for a job to the initial 'pending' state,
-- clearing timestamps, any rejection reason and any hand-off note. Used when
-- clearing translations on a job whose workflow already completed
-- (current_step_id is nil): the job is reopened at its first step instead of
-- being stuck read-only. Assignees are preserved so the reopened first step
-- keeps its owner. See job.HandleClearTranslations (feedback row 63).
UPDATE job_step_assignments
SET status = 'pending', started_at = NULL, completed_at = NULL, rejection_reason = '', handoff_note = NULL
WHERE job_id = $1;

-- name: StartStepAssignment :exec
UPDATE job_step_assignments
SET status = 'in_progress', started_at = now()
WHERE id = $1;

-- name: StartNextStep :exec
-- Internal: caller must have tenant-verified the job (AdvanceJob).
-- Starts the step a hand-off moves the job onto. assigned_to is the person
-- to put on it, or NULL to keep the step's current one.
-- handoff_note is the comment written on completing the step before it; NULL
-- clears a note an earlier hand-off left.
UPDATE job_step_assignments
SET status = 'in_progress', started_at = now(),
    assigned_to = COALESCE(sqlc.narg('assigned_to'), assigned_to),
    handoff_note = sqlc.narg('handoff_note')
WHERE id = @id;

-- name: AssignStepToUser :exec
UPDATE job_step_assignments
SET assigned_to = $2
WHERE id = $1;

-- name: ReassignInFlightStepAssignments :exec
-- Propagate a workflow step's newly-picked default_assignee onto the job step
-- assignments already materialized from that step, so assigning a step to a
-- linguist surfaces the project's in-flight jobs on their Tasks page.
--
-- Upload (file.HandleUpload) and workflow advance (StartNextStep) apply
-- default_assignee only to the assignments they create. This query covers jobs
-- that already exist, which otherwise stay with whoever uploaded the file.
--
-- Only non-completed assignments are touched: completed steps are history, and
-- rewriting their assignee would falsify who actually did the work. The caller
-- fires this only when the edit CHANGED default_assignee to a non-NULL user, so
-- unrelated edits (rename, deadline) and clearing to "Anyone" leave in-flight
-- assignees alone.
--
-- Structurally tenant-scoped through job → file → project rather than trusting
-- the step id, so an edit can never reach another tenant's assignments.
UPDATE job_step_assignments jsa
SET assigned_to = @assigned_to
FROM jobs j, files f, projects p
WHERE jsa.job_id = j.id
  AND f.id = j.file_id
  AND p.id = f.project_id
  AND jsa.workflow_step_id = @workflow_step_id
  AND jsa.status IN ('pending', 'in_progress')
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: SetJobCurrentStep :exec
UPDATE jobs SET current_step_id = $2 WHERE id = $1;

-- name: GetDefaultAssigneeForStep :one
-- Gets the default_assignee from the workflow_step definition for a given job_step_assignment
SELECT ws.default_assignee
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE jsa.id = $1;

-- name: GetNextStepAssignment :one
-- Gets the next pending step after the current one (by step_order)
-- Handles both template-scoped and project-scoped workflow steps.
SELECT jsa.id, jsa.workflow_step_id, jsa.status, jsa.assigned_to
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN job_step_assignments current_jsa ON current_jsa.id = @current_step_id
JOIN workflow_steps current_ws ON current_ws.id = current_jsa.workflow_step_id
WHERE jsa.job_id = current_jsa.job_id
  AND ws.step_order > current_ws.step_order
  AND ws.settings_profile_id = current_ws.settings_profile_id
ORDER BY ws.step_order
LIMIT 1;

-- name: GetPreviousStepAssignment :one
-- Gets the previous step before the current one (by step_order).
-- Skips `ai` steps: a rejection hands work back to a person, and an ai step
-- has none — so the nearest earlier human/hybrid step is reopened instead.
SELECT jsa.id, jsa.workflow_step_id, jsa.status, jsa.assigned_to
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN job_step_assignments current_jsa ON current_jsa.id = @current_step_id
JOIN workflow_steps current_ws ON current_ws.id = current_jsa.workflow_step_id
WHERE jsa.job_id = current_jsa.job_id
  AND ws.step_order < current_ws.step_order
  AND ws.settings_profile_id = current_ws.settings_profile_id
  AND ws.step_type NOT IN ('ai', 'pretranslate')
ORDER BY ws.step_order DESC
LIMIT 1;

-- name: CountActiveAssignmentsForStep :one
-- Counts job step assignments that reference a workflow step (to prevent deletion of active steps)
SELECT count(*) FROM job_step_assignments WHERE workflow_step_id = $1;

-- name: ListActiveStepsForProject :many
-- One representative workflow-step row per job in a project (batched, avoids
-- N+1): the job's current task. DISTINCT ON picks the in_progress step when
-- there is one, otherwise the last completed step, otherwise the latest step —
-- the same rule as taskedit's edit guard and the editor's currentTask, so the
-- matrix cell, Open and the job row agree. Completed-workflow jobs
-- (current_step_id NULL) still surface a step assignment to carry/edit a
-- deadline. `step_order` backs first-step-only gating (Apply TM/MT);
-- `step_assignment_id` + `deadline` back the inline deadline editor; `is_ai_step`
-- gates the manager QE+APE re-run button.
SELECT DISTINCT ON (jsa.job_id)
       jsa.id AS step_assignment_id, jsa.job_id, ws.name AS step_name, ws.step_type,
       ws.step_order AS step_order, jsa.status, jsa.deadline,
       jsa.assigned_to, u.name AS assignee_name,
       (ws.ai_router_id IS NOT NULL)::bool AS is_ai_step,
       (SELECT COUNT(*) FROM workflow_steps ws2
        WHERE ws2.settings_profile_id = ws.settings_profile_id
          AND ws2.step_order > ws.step_order) = 0 AS is_last_step
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE f.project_id = @project_id
  -- job_ids NULL returns every job; otherwise only those jobs.
  AND (sqlc.narg('job_ids')::bigint[] IS NULL OR jsa.job_id = ANY(sqlc.narg('job_ids')::bigint[]))
ORDER BY jsa.job_id, (jsa.status = 'in_progress') DESC, (jsa.status = 'completed') DESC, ws.step_order DESC;

-- name: ListProjectTasks :many
-- Every step assignment of every job in a project: one row per (job, step),
-- the cells of the project page's task matrix. Tenant-scoped through the
-- project row, so another tenant's project id returns nothing.
SELECT jsa.id, jsa.job_id, jsa.workflow_step_id, jsa.status, jsa.deadline,
       jsa.assigned_to, u.name AS assignee_name, jsa.completed_at,
       jsa.rejection_reason, jsa.handoff_note, ws.name AS step_name, ws.step_order, ws.step_type
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE p.id = @project_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
ORDER BY ws.step_order, jsa.job_id;

-- name: UpdateStepAssignmentDeadline :exec
-- Sets or clears (NULL) the deadline on a single step assignment. Tenant +
-- job-ownership are verified in the handler before this runs.
UPDATE job_step_assignments
SET deadline = $2
WHERE id = $1;

-- name: ListJobsForUserStep :many
-- Jobs where the user is assigned to the current active step
SELECT j.*, jsa.id AS step_assignment_id, ws.name AS step_name
FROM jobs j
JOIN job_step_assignments jsa ON jsa.job_id = j.id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE jsa.assigned_to = @user_id
  AND jsa.status = 'in_progress'
  AND j.current_step_id = jsa.id
ORDER BY jsa.started_at DESC;

-- name: CountStartedAssignmentsForStep :one
-- Assignments on a step that a job has already reached (anything but
-- pending). A step nobody reached may still be swapped or removed by a
-- workflow edit; a reached one may not.
SELECT count(*) FROM job_step_assignments WHERE workflow_step_id = $1 AND status <> 'pending';

-- name: ListJobsForSettingsProfile :many
-- Jobs whose step assignments hang off a profile's steps — the jobs a
-- workflow edit on that profile must keep in step with (appended steps get
-- assignments on each of them).
SELECT DISTINCT j.id, j.status, j.current_step_id, j.target_language
FROM jobs j
JOIN job_step_assignments jsa ON jsa.job_id = j.id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE ws.settings_profile_id = $1
ORDER BY j.id;

-- name: ListUnrecordedTextForJob :many
-- Internal: caller must have tenant-verified the job.
--
-- Segments the step is about to hand over whose text history never recorded:
-- a person edited them and never confirmed, so only the draft autosave ran and
-- a draft writes no text row. Without this, the next person to confirm gets an
-- edit distance measured against the pre-edit text and takes credit for work
-- they did not do.
--
-- The LATERAL is the expensive part — one index probe per candidate — so the
-- cheap column filters narrow the set first. On a job where everyone confirmed,
-- `candidates` is empty and no history is read at all.
--
-- `edited_by IS NOT NULL` keeps machine text out: every automation path emits
-- its own row, and APE/QE never set edited_by (UpdateTranslationPredict
-- requires it to be NULL).
WITH candidates AS (
    SELECT t.id, t.active_text, t.active_rich_text, t.edited_by
    FROM translations t
    JOIN segments s ON s.id = t.segment_id
    JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
    WHERE j.id = @job_id
      AND t.status = 'needs_review'
      AND t.edited_by IS NOT NULL
      AND t.active_text IS NOT NULL
      AND t.active_text <> ''
)
SELECT c.id, c.active_text, c.active_rich_text, c.edited_by, last.text AS baseline
FROM candidates c
LEFT JOIN LATERAL (
    SELECT h.text FROM translation_history h
    WHERE h.translation_id = c.id AND h.text IS NOT NULL
    ORDER BY h.created_at DESC, h.id DESC
    LIMIT 1
) last ON true
WHERE last.text IS DISTINCT FROM c.active_text;

-- name: BumpStepRound :one
-- Internal: caller must have tenant-verified the job. Opens the next round of
-- a step assignment and returns its number. ai_review_all: the round's AI run
-- reviews every translated, unlocked segment, people's text included.
UPDATE job_step_assignments SET round = round + 1, ai_review_all = @ai_review_all WHERE id = @id RETURNING round;

-- name: OpenStepRound :exec
-- Internal: caller must have tenant-verified the job. One row per translation
-- of the step's job for the assignment's current round, recording the
-- predict_outcome the round starts with.
INSERT INTO translation_step_statuses (
    tenant_id, translation_id, job_step_assignment_id, round, start_predict_outcome
)
SELECT p.tenant_id, t.id, jsa.id, jsa.round, t.predict_outcome
FROM job_step_assignments jsa
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN segments s ON s.file_id = j.file_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = j.target_language
WHERE jsa.id = @assignment_id
ON CONFLICT (translation_id, job_step_assignment_id, round) DO NOTHING;

-- name: CloseStepRound :exec
-- Internal: caller must have tenant-verified the job. Records how the
-- assignment's current round ends for every translation of the job. Upserts:
-- a translation added mid-round, or a round opened before rounds were
-- recorded, has no start row. For those the AI outcome counts as this round's
-- only when the step runs AI.
INSERT INTO translation_step_statuses (
    tenant_id, translation_id, job_step_assignment_id, round,
    ended_at, end_reason, status, confirmed_via, predict_outcome, human_edited, end_text_md5
)
SELECT p.tenant_id, t.id, jsa.id, jsa.round,
       now(), @end_reason::text, t.status, t.confirmed_via,
       CASE WHEN ws.ai_router_id IS NOT NULL THEN t.predict_outcome END,
       t.step_edited,
       md5(coalesce(t.active_text, '') || chr(31) || coalesce(t.active_rich_text, ''))
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN segments s ON s.file_id = j.file_id
JOIN translations t ON t.segment_id = s.id AND t.target_language = j.target_language
WHERE jsa.id = @assignment_id
ON CONFLICT (translation_id, job_step_assignment_id, round) DO UPDATE SET
    ended_at        = EXCLUDED.ended_at,
    end_reason      = EXCLUDED.end_reason,
    status          = EXCLUDED.status,
    confirmed_via   = EXCLUDED.confirmed_via,
    predict_outcome = CASE
        WHEN translation_step_statuses.start_predict_outcome IS NULL
        THEN (SELECT t2.predict_outcome FROM translations t2 WHERE t2.id = EXCLUDED.translation_id)
    END,
    human_edited    = EXCLUDED.human_edited,
    end_text_md5    = EXCLUDED.end_text_md5;

-- name: ResetStepStatuses :exec
-- Internal: caller must have tenant-verified the job. A new step takes away
-- the previous step's work: approved and auto-approved segments go back to
-- needs_review and every edit mark is cleared, so nothing reads as approved
-- or draft. The AI verdict stays, so the next step sees it came from an
-- earlier step. Locked segments keep their status: a locked row cannot be
-- confirmed. updated_at — the editor's version check — moves only where the
-- status does: the edit mark is display state, so clearing it alone must not
-- fail an open editor's next save.
UPDATE translations t
SET status        = CASE WHEN NOT t.locked AND t.status IN ('approved', 'auto_approved') THEN 'needs_review' ELSE t.status END,
    confirmed_via = CASE WHEN NOT t.locked AND t.status IN ('approved', 'auto_approved') THEN 'user' ELSE t.confirmed_via END,
    step_edited   = false,
    updated_at    = CASE WHEN NOT t.locked AND t.status IN ('approved', 'auto_approved') THEN now() ELSE t.updated_at END
FROM segments s, jobs j
WHERE j.id = @job_id
  AND s.file_id = j.file_id
  AND t.segment_id = s.id
  AND t.target_language = j.target_language
  AND (t.step_edited OR (NOT t.locked AND t.status IN ('approved', 'auto_approved')));

-- name: RestoreStepStatuses :exec
-- Internal: caller must have tenant-verified the job. A reopened step looks as
-- its last closed round left it: status, confirmed_via and the edit mark come
-- back. Text a later step rewrote comes back unconfirmed and marked edited, so
-- it reads as a draft to look at again; with @only_unchanged it is left as it
-- is (a step the job moves forward into again). Locked segments, segments
-- without a recorded round, and an auto-approval whose verdict is gone keep
-- their status.
-- Only rows whose values change are written, and updated_at — the editor's
-- version check — moves only where status or confirmed_via does. The edit
-- that reopens a task carries the version the editor read; a reopen that
-- touched every row would fail that edit, and every other open editor's next
-- save, as a conflict nobody caused.
UPDATE translations t
SET status        = v.status,
    confirmed_via = v.confirmed_via,
    step_edited   = v.step_edited,
    updated_at    = CASE
        WHEN (t.status, t.confirmed_via) IS DISTINCT FROM (v.status, v.confirmed_via) THEN now()
        ELSE t.updated_at
    END
FROM (
    SELECT t2.id,
           CASE WHEN same.text THEN last.status ELSE 'needs_review' END AS status,
           CASE WHEN same.text THEN coalesce(last.confirmed_via, 'user') ELSE 'user' END AS confirmed_via,
           CASE WHEN same.text THEN coalesce(last.human_edited, false) ELSE true END AS step_edited
    FROM translations t2
    JOIN (
        SELECT DISTINCT ON (tss.translation_id)
               tss.translation_id, tss.status, tss.confirmed_via, tss.human_edited, tss.end_text_md5
        FROM translation_step_statuses tss
        WHERE tss.job_step_assignment_id = @assignment_id
          AND tss.ended_at IS NOT NULL
        ORDER BY tss.translation_id, tss.round DESC
    ) last ON last.translation_id = t2.id
    CROSS JOIN LATERAL (
        SELECT md5(coalesce(t2.active_text, '') || chr(31) || coalesce(t2.active_rich_text, '')) = last.end_text_md5 AS text
    ) same
    WHERE NOT t2.locked
      AND last.status IS NOT NULL
      AND (same.text OR NOT @only_unchanged::bool)
      AND (last.status <> 'auto_approved' OR t2.predict_outcome IN ('approved', 'ape_approved'))
) v
WHERE t.id = v.id
  AND (t.status, t.confirmed_via, t.step_edited) IS DISTINCT FROM (v.status, v.confirmed_via, v.step_edited);

-- name: CarryStepPredictions :exec
-- Internal: caller must have tenant-verified the job. An AI verdict the
-- step's last closed round produced still belongs to the step when it is
-- reopened: the new round counts it as its own, not as an earlier step's.
UPDATE translation_step_statuses cur
SET start_predict_outcome = NULL
FROM job_step_assignments jsa, (
    SELECT DISTINCT ON (tss.translation_id) tss.translation_id, tss.predict_outcome
    FROM translation_step_statuses tss
    WHERE tss.job_step_assignment_id = @assignment_id
      AND tss.ended_at IS NOT NULL
    ORDER BY tss.translation_id, tss.round DESC
) last
WHERE jsa.id = @assignment_id
  AND cur.job_step_assignment_id = jsa.id
  AND cur.round = jsa.round
  AND cur.translation_id = last.translation_id
  AND last.predict_outcome IS NOT NULL;

-- name: ClearStepEdited :exec
-- Internal: caller must have tenant-verified the job.
UPDATE translations t
SET step_edited = false
FROM segments s, jobs j
WHERE j.id = @job_id
  AND s.file_id = j.file_id
  AND t.segment_id = s.id
  AND t.target_language = j.target_language
  AND t.step_edited;
