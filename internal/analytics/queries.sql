-- ============================================================
-- Analysis Weights (per-tenant configurable match band weights)
-- ============================================================

-- name: ListAnalysisWeights :many
SELECT * FROM analysis_weights
WHERE tenant_id = $1
ORDER BY band;

-- name: UpsertAnalysisWeight :one
INSERT INTO analysis_weights (tenant_id, band, weight_pct)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, band)
DO UPDATE SET weight_pct = EXCLUDED.weight_pct
RETURNING *;

-- name: DeleteAnalysisWeights :exec
DELETE FROM analysis_weights WHERE tenant_id = $1;

-- ============================================================
-- Productivity Metrics
-- ============================================================

-- name: GetProductivityByProject :many
SELECT
    t.edited_by AS user_id,
    u.name AS user_name,
    t.target_language,
    COUNT(*)::int AS segments_edited,
    COALESCE(SUM(s.word_count), 0)::int AS words_edited,
    COALESCE(SUM(length(s.source_text)), 0)::int AS chars_edited,
    COALESCE(SUM(t.editing_time_ms), 0)::bigint AS total_editing_time_ms,
    COALESCE(AVG(t.edit_distance), 0)::float AS avg_edit_distance,
    COALESCE(AVG(
        CASE WHEN length(s.source_text) > 0
        THEN t.edit_distance::float / length(s.source_text)
        ELSE 0 END
    ), 0)::float AS avg_ter
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN users u ON u.id = t.edited_by
WHERE f.project_id = @project_id
  AND t.edited_by IS NOT NULL
  AND t.editing_time_ms > 0
GROUP BY t.edited_by, u.name, t.target_language
ORDER BY words_edited DESC;

-- name: GetProductivityByTenant :many
SELECT
    t.edited_by AS user_id,
    u.name AS user_name,
    COUNT(*)::int AS segments_edited,
    COALESCE(SUM(s.word_count), 0)::int AS words_edited,
    COALESCE(SUM(length(s.source_text)), 0)::int AS chars_edited,
    COALESCE(SUM(t.editing_time_ms), 0)::bigint AS total_editing_time_ms,
    COALESCE(AVG(t.edit_distance), 0)::float AS avg_edit_distance
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
JOIN users u ON u.id = t.edited_by
WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = @tenant_id)
  AND t.edited_by IS NOT NULL
  AND t.editing_time_ms > 0
  AND t.updated_at >= @from_date
  AND t.updated_at < @to_date
GROUP BY t.edited_by, u.name
ORDER BY words_edited DESC;

-- name: GetStepProductivityByProject :many
-- Per-workflow-step productivity from translation_history.
-- Unlike GetProductivityByProject (which reads translations.edited_by — only
-- the LAST editor), this attributes every human confirm event to the step it
-- happened in, so a translator's work is visible even after a reviewer touches
-- the segment. The CTE collapses repeated edits of the same segment within a
-- (step, user) group to one row first, so words_edited counts each segment once
-- while total_editing_time_ms sums across all of that segment's edit events.
WITH per_translation AS (
    SELECT
        th.job_step_assignment_id,
        th.created_by,
        th.translation_id,
        SUM(COALESCE(th.editing_time_ms, 0)) AS editing_time_ms,
        AVG(th.edit_distance) AS avg_edit_distance
    -- Starts from the project's step assignments, so the history read is
    -- bounded by the project, not by the whole table.
    FROM jobs pj
    JOIN files pf ON pf.id = pj.file_id AND pf.project_id = @project_id
    JOIN job_step_assignments pjsa ON pjsa.job_id = pj.id
    JOIN translation_history th ON th.job_step_assignment_id = pjsa.id
    -- 'approved' counts alongside 'human': a reviewer who reads a segment and
    -- confirms it unchanged did that step's work. Those rows carry no edit
    -- distance, and AVG ignores NULLs, so avg_edit_distance stays a measure of
    -- actual rewriting.
    WHERE th.kind IN ('human', 'approved')
      AND th.created_by IS NOT NULL
    GROUP BY th.job_step_assignment_id, th.created_by, th.translation_id
)
SELECT
    jsa.id AS step_assignment_id,
    ws.name AS step_name,
    ws.step_order,
    pt.created_by AS user_id,
    u.name AS user_name,
    t.target_language,
    COUNT(*)::int AS segments_edited,
    COALESCE(SUM(s.word_count), 0)::int AS words_edited,
    COALESCE(SUM(length(s.source_text)), 0)::int AS chars_edited,
    COALESCE(SUM(pt.editing_time_ms), 0)::bigint AS total_editing_time_ms,
    COALESCE(AVG(pt.avg_edit_distance), 0)::float AS avg_edit_distance
FROM per_translation pt
JOIN translations t ON t.id = pt.translation_id
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN job_step_assignments jsa ON jsa.id = pt.job_step_assignment_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN users u ON u.id = pt.created_by
WHERE f.project_id = @project_id
GROUP BY jsa.id, ws.name, ws.step_order, pt.created_by, u.name, t.target_language
ORDER BY ws.step_order, words_edited DESC;

-- ── Attention and progress (MCP insight tools) ──────────────────────────────
-- Every query here is tenant-scoped structurally through projects.tenant_id.
-- The attention lists look at in_progress projects only: a draft has not
-- started, and on_hold / completed work is not expected to move.

-- name: ListUnassignedCurrentSteps :many
-- Jobs whose current workflow step needs a person and has nobody assigned.
-- AI steps run without an assignee, so they are left out.
SELECT
    j.id              AS job_id,
    p.id              AS project_id,
    p.name            AS project_name,
    f.name            AS file_name,
    j.target_language AS target_language,
    ws.name           AS step_name,
    jsa.deadline      AS step_deadline
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND p.status = 'in_progress'
  AND j.status NOT IN ('completed', 'failed')
  AND jsa.status IN ('pending', 'in_progress')
  AND jsa.assigned_to IS NULL
  AND ws.step_type NOT IN ('ai', 'pretranslate')
ORDER BY jsa.deadline ASC NULLS LAST, j.id
LIMIT @row_limit;

-- name: ListStaleJobs :many
-- Open jobs with no activity since @before. Activity is the later of the job
-- row's own update (status, step, pipeline) and the newest translation edit,
-- read from job_translation_stats.
SELECT
    j.id              AS job_id,
    p.id              AS project_id,
    p.name            AS project_name,
    f.name            AS file_name,
    j.target_language AS target_language,
    j.status          AS status,
    j.current_step    AS current_step,
    GREATEST(j.updated_at, COALESCE(te.last_edit, j.updated_at))::timestamptz AS last_activity_at
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN LATERAL (
    SELECT max(st.last_activity_at) AS last_edit
    FROM job_translation_stats st
    WHERE st.file_id = j.file_id AND st.target_language = j.target_language
) te ON true
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND p.status = 'in_progress'
  AND j.status NOT IN ('completed', 'failed')
  -- Implied by the GREATEST check below; it is here so the lateral max() only
  -- runs for jobs that can still qualify.
  AND j.updated_at < @before::timestamptz
  AND GREATEST(j.updated_at, COALESCE(te.last_edit, j.updated_at)) < @before::timestamptz
ORDER BY last_activity_at ASC, j.id
LIMIT @row_limit;

-- name: ListFailedJobs :many
-- Jobs whose pipeline ended in an error, newest first.
SELECT
    j.id              AS job_id,
    p.id              AS project_id,
    p.name            AS project_name,
    f.name            AS file_name,
    j.target_language AS target_language,
    j.current_step    AS current_step,
    j.error_message   AS error_message,
    j.updated_at      AS failed_at
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND p.status = 'in_progress'
  AND j.status = 'failed'
ORDER BY j.updated_at DESC, j.id
LIMIT @row_limit;

-- name: ListQAErrorHotspots :many
-- Open jobs ranked by unresolved (not ignored) QA errors.
SELECT
    j.id              AS job_id,
    p.id              AS project_id,
    p.name            AS project_name,
    f.name            AS file_name,
    j.target_language AS target_language,
    count(*)::bigint  AS error_count
FROM qa_results qr
JOIN translations t ON t.id = qr.translation_id
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
JOIN jobs j ON j.file_id = f.id AND j.target_language = t.target_language
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND p.status = 'in_progress'
  AND j.status <> 'completed'
  AND qr.severity = 'error'
  AND NOT qr.ignored
GROUP BY j.id, p.id, p.name, f.name, j.target_language
ORDER BY error_count DESC, j.id
LIMIT @row_limit;

-- name: ListProgressProjectPage :many
-- One page of project ids for the progress report, newest first. A project_id
-- of 0 means every project; cursor 0 means the first page.
SELECT id
FROM projects
WHERE tenant_id = @tenant_id AND deleted_at IS NULL
  AND (@project_id::bigint = 0 OR id = @project_id::bigint)
  AND (@cursor::bigint = 0 OR id < @cursor::bigint)
ORDER BY id DESC
LIMIT @row_limit;

-- name: GetTranslationProgress :many
-- Segment and word counts per project × target language, split by where each
-- translation stands, from job_translation_stats. The buckets do not overlap:
--   untranslated   needs_review with no or empty target text
--   pre_translated needs_review with text nobody has edited (TM / MT fill)
--   draft          needs_review with text a person edited but did not confirm
--   auto_approved  approved by quality prediction, no human confirm
--   confirmed      confirmed by a person
SELECT
    p.id                AS project_id,
    p.name              AS project_name,
    st.target_language  AS target_language,
    sum(st.segments)::bigint AS total_segments,
    COALESCE(sum(st.words), 0)::bigint AS total_words,
    COALESCE(sum(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state < 2), 0)::bigint AS untranslated_segments,
    COALESCE(sum(st.words) FILTER (WHERE st.status = 'needs_review' AND st.text_state < 2), 0)::bigint AS untranslated_words,
    COALESCE(sum(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state = 2 AND NOT st.edited), 0)::bigint AS pre_translated_segments,
    COALESCE(sum(st.words) FILTER (WHERE st.status = 'needs_review' AND st.text_state = 2 AND NOT st.edited), 0)::bigint AS pre_translated_words,
    COALESCE(sum(st.segments) FILTER (WHERE st.status = 'needs_review' AND st.text_state = 2 AND st.edited), 0)::bigint AS draft_segments,
    COALESCE(sum(st.words) FILTER (WHERE st.status = 'needs_review' AND st.text_state = 2 AND st.edited), 0)::bigint AS draft_words,
    COALESCE(sum(st.segments) FILTER (WHERE st.status = 'auto_approved'), 0)::bigint AS auto_approved_segments,
    COALESCE(sum(st.words) FILTER (WHERE st.status = 'auto_approved'), 0)::bigint AS auto_approved_words,
    COALESCE(sum(st.segments) FILTER (WHERE st.status = 'approved'), 0)::bigint AS confirmed_segments,
    COALESCE(sum(st.words) FILTER (WHERE st.status = 'approved'), 0)::bigint AS confirmed_words
FROM projects p
JOIN files f ON f.project_id = p.id
JOIN job_translation_stats st ON st.file_id = f.id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND p.id = ANY(@project_ids::bigint[])
GROUP BY p.id, p.name, st.target_language
HAVING sum(st.segments) > 0
ORDER BY p.id DESC, st.target_language;
