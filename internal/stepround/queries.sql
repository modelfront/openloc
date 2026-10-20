-- name: GetRollbackTarget :one
-- Internal: caller must have tenant-verified the job of @current_step_id. The
-- step assignment @target_id, if it belongs to the same job and comes before
-- the current step in the same workflow.
SELECT jsa.id, jsa.workflow_step_id, jsa.assigned_to, ws.name AS step_name, ws.step_type,
       ws.step_order, (ws.ai_router_id IS NOT NULL)::bool AS has_ai
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN job_step_assignments cur ON cur.id = @current_step_id
JOIN workflow_steps cws ON cws.id = cur.workflow_step_id
WHERE jsa.id = @target_id
  AND jsa.job_id = cur.job_id
  AND ws.settings_profile_id = cws.settings_profile_id
  AND ws.step_order < cws.step_order;

-- name: ResetStepsBetween :exec
-- Internal: caller must have tenant-verified the job. The steps between a
-- rollback's target and the step it leaves are not started again; their
-- rounds stay on record.
UPDATE job_step_assignments jsa
SET status = 'pending', started_at = NULL, completed_at = NULL, rejection_reason = '', handoff_note = NULL
FROM workflow_steps ws, job_step_assignments cur, workflow_steps cws
WHERE cur.id = @current_step_id
  AND cws.id = cur.workflow_step_id
  AND jsa.job_id = cur.job_id
  AND ws.id = jsa.workflow_step_id
  AND ws.settings_profile_id = cws.settings_profile_id
  AND ws.step_order > @target_step_order::int
  AND ws.step_order < cws.step_order;

-- name: ClearUndoneVerdicts :exec
-- Internal: caller must have tenant-verified the job. Before a step reopens,
-- drops the AI verdict (and its QE score) of every unlocked segment where it
-- no longer describes the segment as that step left it: the text changed
-- since, or a later step's AI gave the verdict. A later AI step only scores
-- segments without a verdict, so clearing it leaves what the step had.
UPDATE translations t
SET predict_outcome = NULL, qe_note = NULL
FROM (
    SELECT DISTINCT ON (tss.translation_id)
           tss.translation_id, tss.ended_at, tss.end_text_md5
    FROM translation_step_statuses tss
    WHERE tss.job_step_assignment_id = @assignment_id
      AND tss.ended_at IS NOT NULL
    ORDER BY tss.translation_id, tss.round DESC
) last, job_step_assignments target, workflow_steps tws
WHERE t.id = last.translation_id
  AND target.id = @assignment_id
  AND tws.id = target.workflow_step_id
  AND NOT t.locked
  AND (t.predict_outcome IS NOT NULL OR t.qe_note IS NOT NULL)
  AND (
    md5(coalesce(t.active_text, '') || chr(31) || coalesce(t.active_rich_text, '')) IS DISTINCT FROM last.end_text_md5
    OR EXISTS (
        SELECT 1
        FROM translation_step_statuses later
        JOIN job_step_assignments ljsa ON ljsa.id = later.job_step_assignment_id
        JOIN workflow_steps lws ON lws.id = ljsa.workflow_step_id
        WHERE later.translation_id = t.id
          AND ljsa.job_id = target.job_id
          AND lws.step_order > tws.step_order
          AND later.predict_outcome IS NOT NULL
          AND later.ended_at > last.ended_at
    )
  );

-- name: ResetForAIReview :exec
-- Internal: caller must have tenant-verified the job. A step whose AI runs
-- again starts with every translated, unlocked segment unconfirmed and
-- without a verdict, so the run reviews them all on their current text.
UPDATE translations t
SET status          = 'needs_review',
    confirmed_via   = 'user',
    step_edited     = false,
    predict_outcome = NULL,
    qe_note         = NULL,
    updated_at      = now()
FROM segments s, jobs j
WHERE j.id = @job_id
  AND s.file_id = j.file_id
  AND t.segment_id = s.id
  AND t.target_language = j.target_language
  AND NOT t.locked
  AND coalesce(t.active_text, '') <> '';
