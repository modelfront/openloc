-- name: InsertMCPToolCall :exec
-- Audit row for one MCP tool invocation. Written for every outcome,
-- including 'denied', so a role/tenant rejection is as visible as a success.
--
-- args_digest is a SHA-256 of the canonical arguments, never the arguments
-- themselves: tool args can carry source text, and this table must not become a
-- second copy of customer content.
INSERT INTO mcp_tool_calls (
    tenant_id, user_id, token_id, tool, args_digest, outcome, error_code, duration_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListMCPToolCalls :many
-- Tenant-scoped audit read for the org settings UI and for support ("what did
-- the agent actually call?"). Cursor-paginated on descending id.
SELECT c.id, c.tenant_id, c.user_id, c.token_id, c.tool, c.args_digest,
       c.outcome, c.error_code, c.duration_ms, c.created_at,
       u.email AS user_email,
       t.name  AS token_name
FROM mcp_tool_calls c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN api_tokens t ON t.id = c.token_id
WHERE c.tenant_id = $1
  AND (sqlc.arg(cursor)::bigint = 0 OR c.id < sqlc.arg(cursor)::bigint)
ORDER BY c.id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: GetMCPJobDetail :one
-- Tenant-scoped job detail in one round trip: job + its file + its project.
--
-- Scoping is structural — the join chain jobs→files→projects ends at
-- projects.tenant_id, so a job in another tenant yields no row rather than
-- relying on a caller to remember a WHERE clause. ErrNoRows is the only
-- cross-tenant signal, which is exactly the 404 the MCP layer surfaces.
SELECT
    j.id              AS job_id,
    j.file_id         AS file_id,
    j.target_language AS target_language,
    j.status          AS status,
    j.current_step    AS current_step,
    j.word_count      AS word_count,
    j.error_message   AS error_message,
    j.mt_skip_reason  AS mt_skip_reason,
    j.translated_count AS translated_count,
    j.total_count     AS total_count,
    f.name            AS file_name,
    f.format          AS file_format,
    f.segment_count   AS segment_count,
    p.id              AS project_id,
    p.name            AS project_name,
    p.source_language AS source_language,
    p.settings_profile_id AS settings_profile_id,
    p.deadline        AS project_deadline,
    -- A job's EFFECTIVE deadline is its active step's deadline:
    -- jobs.deadline was dropped in migration 046 and there is deliberately NO
    -- project fallback. Selecting both separately keeps them from being
    -- conflated — an earlier version of this query exposed only the project
    -- deadline and the tool reported it as the job's, which is worse than
    -- reporting nothing.
    jsa.deadline      AS step_deadline
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
WHERE j.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: ListMCPJobs :many
-- Jobs in one project — or org-wide when project_id=0 — with the EFFECTIVE deadline (the active step's).
--
-- Purpose-built rather than reusing job.ListJobsByProject for two reasons: that
-- query selects no deadline at all (so "any overdue jobs?" was unanswerable),
-- and it takes a bare project_id with no tenant scoping, forcing callers to
-- remember a preceding tenant check. Here the scoping is structural via
-- projects.tenant_id, and the deadline comes from job_step_assignments joined
-- through jobs.current_step_id.
--
-- Ordered deadline-first (NULLS LAST) to match the app's own jobs list, so the
-- soonest-due work is what a model sees first.
--
-- Keyset pagination on that order: after_id is the last job of the previous
-- page and after_deadline its effective deadline ('infinity' when it has none),
-- both taken from the client's cursor: they are only a sort position, never an
-- ownership check, and the tenant filter bounds the rows. after_id = 0 is the
-- first page.
-- The row comparison mirrors the ORDER BY: deadline ascending with no deadline
-- last, then id descending.
SELECT
    j.id              AS job_id,
    j.file_id         AS file_id,
    j.target_language AS target_language,
    j.status          AS status,
    j.current_step    AS current_step,
    j.word_count      AS word_count,
    j.error_message   AS error_message,
    j.mt_skip_reason  AS mt_skip_reason,
    j.translated_count AS translated_count,
    j.total_count     AS total_count,
    f.name            AS file_name,
    f.segment_count   AS segment_count,
    jsa.deadline      AS step_deadline,
    p.id              AS project_id,
    p.name            AS project_name
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
-- project_id = 0 means ALL projects in the tenant:
-- "any overdue jobs anywhere?" must be one call, and the overdue predicate
-- must run server-side — an org-wide listing can exceed the row cap, and a
-- model filtering a TRUNCATED list would silently miss overdue work.
WHERE (sqlc.arg(project_id)::bigint = 0 OR f.project_id = sqlc.arg(project_id)::bigint)
  AND p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND (sqlc.arg(status)::text = '' OR j.status = sqlc.arg(status)::text)
  -- Overdue = effective (step) deadline passed and the work isn't done.
  -- Completed jobs are excluded; failed ones are NOT — a failed job past its
  -- deadline is exactly what an overdue query exists to surface.
  AND (NOT sqlc.arg(overdue_only)::bool
       OR (jsa.deadline IS NOT NULL AND jsa.deadline < now() AND j.status != 'completed'))
  -- assignee_id = 0 means anyone; otherwise jobs with any step assigned to them.
  AND (sqlc.arg(assignee_id)::bigint = 0 OR EXISTS (
       SELECT 1 FROM job_step_assignments a
       WHERE a.job_id = j.id AND a.assigned_to = sqlc.arg(assignee_id)::bigint))
  AND (sqlc.arg(after_id)::bigint = 0
       OR (COALESCE(jsa.deadline, 'infinity'::timestamptz), -j.id)
          > (sqlc.arg(after_deadline)::timestamptz, -sqlc.arg(after_id)::bigint))
ORDER BY COALESCE(jsa.deadline, 'infinity'::timestamptz) ASC, j.id DESC
LIMIT sqlc.arg(row_limit)::int;

-- name: ListMCPProjectWorkflowSteps :many
-- The workflow step names of a page of projects, in step order, so the project
-- list can say which projects have more than one step. Scoped structurally
-- through projects.tenant_id.
SELECT p.id AS project_id, ws.name
FROM projects p
JOIN workflow_steps ws ON ws.settings_profile_id = p.settings_profile_id
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND p.id = ANY(sqlc.arg(project_ids)::bigint[])
ORDER BY p.id, ws.step_order, ws.id;

-- name: ListMCPProjectMTRouters :many
-- The MT router each of a page of projects translates through, with the name
-- of its default engine ('' when it has none). Scoped structurally through
-- projects.tenant_id, and the router and engines must be the same tenant's. A
-- retired router still shows: its projects still translate through it. A
-- deleted engine does not: the pipeline cannot load it, so it reads as ''.
SELECT p.id AS project_id, r.id AS router_id, r.name AS router_name,
       COALESCE(dme.name, dae.name, '')::text AS default_engine
FROM projects p
JOIN settings_profiles sp ON sp.id = p.settings_profile_id
JOIN mt_routers r ON r.id = sp.mt_router_id AND r.tenant_id = p.tenant_id
LEFT JOIN mt_engines dme ON dme.id = r.default_mt_engine_id AND dme.tenant_id = r.tenant_id AND dme.deleted_at IS NULL
LEFT JOIN ai_engines dae ON dae.id = r.default_ai_engine_id AND dae.tenant_id = r.tenant_id AND dae.deleted_at IS NULL
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND p.id = ANY(sqlc.arg(project_ids)::bigint[]);

-- name: ListMCPMTRouterRules :many
-- The per-language rules of a set of routers with each rule's engine name, in
-- router order ('' for a deleted engine, as for the default above). Scoped
-- through mt_routers.tenant_id.
SELECT rr.router_id, rr.target_lang, COALESCE(me.name, ae.name, '')::text AS engine
FROM mt_router_rules rr
JOIN mt_routers r ON r.id = rr.router_id
LEFT JOIN mt_engines me ON me.id = rr.mt_engine_id AND me.tenant_id = r.tenant_id AND me.deleted_at IS NULL
LEFT JOIN ai_engines ae ON ae.id = rr.ai_engine_id AND ae.tenant_id = r.tenant_id AND ae.deleted_at IS NULL
WHERE r.tenant_id = sqlc.arg(tenant_id)::bigint
  AND rr.router_id = ANY(sqlc.arg(router_ids)::bigint[])
ORDER BY rr.router_id, rr.sort_order, rr.id;

-- name: ListMCPUpcomingSteps :many
-- The caller's steps that have not started: assigned to them, still pending,
-- on a job that is not completed, with the job's active step they wait for (empty when none is
-- active). Scoped by tenant AND user; soonest deadline first.
SELECT j.id AS job_id, p.name AS project_name, f.name AS file_name,
       j.target_language, ws.name AS step_name, jsa.deadline,
       COALESCE(cws.name, '')::text AS current_step
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN job_step_assignments cur ON cur.id = j.current_step_id
LEFT JOIN workflow_steps cws ON cws.id = cur.workflow_step_id
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND jsa.assigned_to = sqlc.arg(user_id)::bigint
  AND jsa.status = 'pending'
  -- A completed job has no work left, whatever its step rows say.
  AND j.status <> 'completed'
ORDER BY jsa.deadline ASC NULLS LAST, j.id DESC, ws.step_order
LIMIT sqlc.arg(row_limit)::int;

-- name: ListMCPJobSteps :many
-- Every workflow step of a page of jobs, with its assignee, in step order.
-- Scoped structurally through projects.tenant_id.
SELECT jsa.job_id, ws.name, ws.step_order, ws.step_type, jsa.status,
       jsa.assigned_to, u.name AS assignee_name, jsa.deadline
FROM job_step_assignments jsa
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN jobs j ON j.id = jsa.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN users u ON u.id = jsa.assigned_to
WHERE p.tenant_id = sqlc.arg(tenant_id)::bigint AND p.deleted_at IS NULL
  AND jsa.job_id = ANY(sqlc.arg(job_ids)::bigint[])
ORDER BY jsa.job_id, ws.step_order, jsa.id;

-- name: ListMCPMembers :many
-- One page of members (or one member, with @user_id), enriched with what
-- vendor questions need:
-- language pairs and a live assignment count. Purpose-built like
-- ListMCPProjects — the REST ListMembersByTenant serves a UI that joins
-- pairs/assignments client-side from other endpoints; a model cannot.
--
-- language_pairs are 'source→target' strings (the arrow never appears in a
-- locale code, unlike '-'). active_jobs counts jobs whose CURRENT step is
-- assigned to the user and in progress — "working right now", not "ever
-- assigned". Both aggregates are tenant-scoped: pairs by ulp.tenant_id (a
-- user's pairs in org B must not leak into org A's listing), assignments
-- structurally through projects.
SELECT
    u.id   AS id,
    u.name AS name,
    m.role AS role,
    COALESCE(lp.pairs, '{}'::text[])::text[] AS language_pairs,
    COALESCE(aj.active_jobs, 0)::bigint AS active_jobs
FROM memberships m
JOIN users u ON u.id = m.user_id
LEFT JOIN LATERAL (
    SELECT array_agg(ulp.source_lang || '→' || ulp.target_lang
                     ORDER BY ulp.source_lang, ulp.target_lang) AS pairs
    FROM user_language_pairs ulp
    WHERE ulp.user_id = u.id
) lp ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS active_jobs
    FROM job_step_assignments jsa
    JOIN jobs j     ON j.current_step_id = jsa.id
    JOIN files f    ON f.id = j.file_id
    JOIN projects p ON p.id = f.project_id
    WHERE jsa.assigned_to = u.id
      AND jsa.status = 'in_progress'
      AND p.tenant_id = m.tenant_id AND p.deleted_at IS NULL
) aj ON true
WHERE m.tenant_id = sqlc.arg(tenant_id)::bigint
  AND u.is_active = true
  AND (sqlc.arg(user_id)::bigint = 0 OR u.id = sqlc.arg(user_id)::bigint)
  -- Keyset page in (name, id) order, after the previous page's last
  -- (name, id) as the cursor carries it. Nothing else is looked up, so the
  -- page does not depend on that member still being here, or on any user
  -- outside the organization.
  AND (NOT sqlc.arg(has_cursor)::bool
       OR (u.name, u.id) > (sqlc.arg(after_name)::text, sqlc.arg(after_id)::bigint))
ORDER BY u.name, u.id
LIMIT sqlc.arg(lim)::int;

-- name: ListMCPFilesPage :many
-- A page of the project's files, newest first; @cursor is the last file id of
-- the previous page (0 = first page). The caller has tenant-checked the project.
SELECT * FROM files
WHERE project_id = @project_id
  AND (@cursor::bigint = 0 OR id < @cursor::bigint)
ORDER BY id DESC
LIMIT @lim::int;

-- name: CountJobsByFiles :many
-- Job count per file for a page of files the caller has tenant-checked.
SELECT file_id, count(*)::int AS job_count
FROM jobs
WHERE file_id = ANY(@file_ids::bigint[])
GROUP BY file_id;
