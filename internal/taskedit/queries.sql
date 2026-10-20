-- Task edit guard (taskedit.Guard). Both reads return the same columns: the
-- job's pipeline state, its in-progress task, and its last completed task,
-- which is the current task once nothing is in progress. One query per check,
-- because drafts autosave every few seconds. Tenant-scoped through the
-- project; no row means no such job in this tenant.

-- name: GetTaskEditStateForTranslation :one
-- Keyed by the translation, so the job comes from the translation's own
-- segment and language, never from a client-supplied file id.
SELECT j.id AS job_id, j.status AS job_status, j.current_step AS job_current_step,
       j.target_language, f.id AS file_id, f.name AS file_name, f.project_id,
       EXISTS (SELECT 1 FROM job_step_assignments x WHERE x.job_id = j.id)::bool AS has_workflow,
       COALESCE(cur.id, 0)::bigint AS active_id,
       COALESCE(cur.is_ai_step, false)::bool AS active_is_ai_step,
       COALESCE(cur.step_type, '')::text AS active_step_type,
       COALESCE(cur.step_order, 0)::int AS active_step_order,
       COALESCE(lc.id, 0)::bigint AS last_completed_id,
       COALESCE(lc.step_order, 0)::int AS last_completed_order,
       COALESCE(lc.name, '')::text AS last_completed_name,
       COALESCE(lc.step_type, '')::text AS last_completed_type
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN LATERAL (
    SELECT jsa.id, ws.step_order, ws.step_type, (ws.ai_router_id IS NOT NULL) AS is_ai_step
    FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND jsa.status = 'in_progress'
    ORDER BY ws.step_order
    LIMIT 1
) cur ON true
LEFT JOIN LATERAL (
    SELECT jsa.id, ws.step_order, ws.name, ws.step_type
    FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND jsa.status = 'completed'
    ORDER BY ws.step_order DESC
    LIMIT 1
) lc ON true
WHERE t.id = @translation_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: GetTaskEditState :one
SELECT j.id AS job_id, j.status AS job_status, j.current_step AS job_current_step,
       j.target_language, f.id AS file_id, f.name AS file_name, f.project_id,
       EXISTS (SELECT 1 FROM job_step_assignments x WHERE x.job_id = j.id)::bool AS has_workflow,
       COALESCE(cur.id, 0)::bigint AS active_id,
       COALESCE(cur.is_ai_step, false)::bool AS active_is_ai_step,
       COALESCE(cur.step_type, '')::text AS active_step_type,
       COALESCE(cur.step_order, 0)::int AS active_step_order,
       COALESCE(lc.id, 0)::bigint AS last_completed_id,
       COALESCE(lc.step_order, 0)::int AS last_completed_order,
       COALESCE(lc.name, '')::text AS last_completed_name,
       COALESCE(lc.step_type, '')::text AS last_completed_type
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN LATERAL (
    SELECT jsa.id, ws.step_order, ws.step_type, (ws.ai_router_id IS NOT NULL) AS is_ai_step
    FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND jsa.status = 'in_progress'
    ORDER BY ws.step_order
    LIMIT 1
) cur ON true
LEFT JOIN LATERAL (
    SELECT jsa.id, ws.step_order, ws.name, ws.step_type
    FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND jsa.status = 'completed'
    ORDER BY ws.step_order DESC
    LIMIT 1
) lc ON true
WHERE j.id = @job_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: LockJobForTaskEdit :one
-- Serialises reopen-by-edit on one job: concurrent first edits on a completed
-- task queue here, and the loser re-reads the state and finds it reopened.
SELECT j.id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = @job_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
FOR UPDATE OF j;

-- name: LockJobsForWrite :many
-- The jobs' rows, locked in id order. A write to many of a job's translations
-- takes this first, so it queues with a step change, which takes
-- LockJobForTaskEdit, instead of deadlocking on the translations the step
-- change's commit-time recount locks. NO KEY UPDATE: rows that only point to
-- the job (a segment hold, an assignment) can still be inserted.
SELECT j.id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE j.id = ANY(@job_ids::bigint[]) AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
ORDER BY j.id
FOR NO KEY UPDATE OF j;

-- name: LockProjectJobsForWrite :many
-- LockJobsForWrite for every job of a project.
SELECT j.id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE f.project_id = @project_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
ORDER BY j.id
FOR NO KEY UPDATE OF j;

-- name: LockProfileJobsForWrite :many
-- LockJobsForWrite for every job with a task on a settings profile's steps.
SELECT j.id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM job_step_assignments jsa
    JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
    WHERE jsa.job_id = j.id AND ws.settings_profile_id = @settings_profile_id
  )
ORDER BY j.id
FOR NO KEY UPDATE OF j;

-- name: ReopenCompletedStepAssignment :execrows
-- Puts a completed task back in progress. Refuses (0 rows) when the task is no
-- longer completed or another task of the job is already in progress, so a job
-- never has two tasks in progress.
UPDATE job_step_assignments jsa
SET status = 'in_progress', started_at = now(), completed_at = NULL
WHERE jsa.id = @id
  AND jsa.status = 'completed'
  AND NOT EXISTS (
      SELECT 1 FROM job_step_assignments o
      WHERE o.job_id = jsa.job_id AND o.status = 'in_progress'
  );

-- name: MarkJobReopened :execrows
-- A completed job is open again because its last task was reopened. The idle
-- label ends in "complete", so the job does not read as running.
UPDATE jobs SET status = 'in_progress', current_step = @current_step, error_message = NULL,
       updated_at = now()
WHERE id = @id AND status = 'completed';
