-- name: ListProjects :many
-- Internal: unfiltered, no pagination (seed tooling and tests). UI uses
-- ListProjectsFiltered.
SELECT * FROM projects WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC;

-- name: ListProjectsFiltered :many
-- Paginated + filtered project list for the UI.
-- All filters are optional (NULL → no filter); tenant_id is always applied.
-- Sort by id DESC for stable cursor pagination (newest-created first).
-- Caller passes limit+1 to detect has_more in the handler.
-- created_by_name is the creator's display name, resolved here so a list row
-- arrives ready to render (same denormalize-a-name-into-the-payload
-- pattern as customer_name in ListTMCollections). Precedence:
--   1. users.name
--   2. users.email  — when the name is blank (the column defaults to an empty
--                     string and signup/SSO paths don't always set one)
--   3. blank        — when there is no user at all: created_by is NULL (row
--                     predates attribution) or the user was deleted
SELECT p.*,
  COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name,
  COALESCE(cv.value, '')::text AS customer_name,
  cv.id AS customer_value_id
FROM projects p
LEFT JOIN users u ON u.id = p.created_by
-- Customer is a built-in label, so the row's customer is
-- the value it picked. Denormalized here for the same reason created_by_name
-- is: a list row should arrive ready to render.
LEFT JOIN label_definitions cd ON cd.tenant_id = p.tenant_id
                              AND cd.system_key = 'customer' AND cd.deleted_at IS NULL
LEFT JOIN project_labels cpl ON cpl.project_id = p.id AND cpl.label_definition_id = cd.id
LEFT JOIN label_values cv ON cv.id = cpl.label_value_id AND cv.deleted_at IS NULL
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND (@cursor::bigint = 0 OR p.id < @cursor)
  AND (sqlc.narg('status')::project_status IS NULL OR p.status = sqlc.narg('status')::project_status)
  -- customer_id filters by the Customer label's value id.
  AND (sqlc.narg('customer_id')::bigint IS NULL OR cpl.label_value_id = sqlc.narg('customer_id')::bigint)
  AND (sqlc.narg('source_lang')::text IS NULL OR p.source_language = sqlc.narg('source_lang')::text)
  AND (sqlc.narg('target_lang')::text IS NULL OR p.target_languages @> ARRAY[sqlc.narg('target_lang')::text])
  AND (sqlc.narg('created_by')::bigint IS NULL OR p.created_by = sqlc.narg('created_by')::bigint)
  AND (sqlc.narg('q')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('q')::text || '%')
ORDER BY p.id DESC
LIMIT @lim;

-- name: ListProjectCreators :many
-- Distinct creators of the tenant's projects — the option set for the list's
-- "Created by" filter.
--
-- Why a dedicated query rather than reusing /api/members: a member who never
-- created a project is a dead filter option (it can only produce "no results"),
-- and the member list is full of them — including leftover test accounts. The
-- TM and term-base lists derive their options from the rows on screen, but this
-- list is cursor-paginated, so its rows can't see creators on later pages. This
-- is authoritative across every page.
--
-- Joins through users (not LEFT), so unattributed projects contribute nothing —
-- there's no id to filter by. Selects u.id rather than p.created_by so the
-- column types as NOT NULL. Deactivated users are included on purpose: their
-- projects still show their name, so the filter must be able to reach them.
SELECT DISTINCT u.id,
  COALESCE(NULLIF(u.name, ''), u.email, '')::text AS name
FROM projects p
JOIN users u ON u.id = p.created_by
WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
ORDER BY COALESCE(NULLIF(u.name, ''), u.email, '')::text;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateProject :one
-- Customer_id, status, created_by are nullable / have a DEFAULT.
-- Caller passes nil/zero for customer_id and created_by when not set, and
-- the literal default for status if the request didn't specify one — sqlc
-- can't elide columns from an INSERT, so we list them all explicitly.
-- deadline is nullable and settable at create: whoever can create a project
-- can set its initial deadline; modifying it later is admin-only via
-- UpdateProjectMetadata. client + domain are free-text taxonomy labels.
INSERT INTO projects (
  name, description, source_language, target_languages,
  tenant_id, settings_profile_id,
  customer_id, status, created_by, deadline,
  client, domain
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateProjectMetadata :one
-- Partial PATCH. Each sqlc.narg field is "absent → keep" via the
-- COALESCE-with-NULL-sentinel pattern. customer_id and deadline have their
-- own clear-flags because COALESCE can't distinguish "absent" from "set to
-- NULL" — the handler passes clear_customer/clear_deadline=true to clear
-- (sets the column to NULL), otherwise the column keeps its current value
-- OR is set to the new one.
-- Client + domain follow description's COALESCE pattern — absent
-- keeps, present (incl. empty string) sets. Empty string is the "clear"
-- signal at the handler boundary (readers treat "" and NULL identically),
-- so a separate clear-flag isn't worth its weight.
UPDATE projects
SET
  name         = COALESCE(sqlc.narg('name')::text, name),
  description  = COALESCE(sqlc.narg('description')::text, description),
  status       = COALESCE(sqlc.narg('status')::project_status, status),
  client       = COALESCE(sqlc.narg('client')::text, client),
  domain       = COALESCE(sqlc.narg('domain')::text, domain),
  customer_id  = CASE
                   WHEN @clear_customer::bool THEN NULL
                   ELSE COALESCE(sqlc.narg('customer_id')::bigint, customer_id)
                 END,
  deadline     = CASE
                   WHEN @clear_deadline::bool THEN NULL
                   ELSE COALESCE(sqlc.narg('deadline')::timestamptz, deadline)
                 END,
  updated_at   = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectDraft :one
-- Full replace of a draft's metadata — the wizard autosaves its whole form, so
-- unlike UpdateProjectMetadata there is no absent-keeps. The status predicate
-- is what keeps this off live projects: a non-draft row is ErrNoRows → 404.
UPDATE projects
SET
  name             = $3,
  source_language  = $4,
  target_languages = $5,
  customer_id      = $6,
  deadline         = $7,
  client           = $8,
  domain           = $9,
  updated_at       = now()
WHERE id = $1 AND tenant_id = $2 AND status = 'draft' AND deleted_at IS NULL
RETURNING *;

-- name: PublishProjectDraft :one
-- draft → in_progress. The handler validates the required fields first;
-- projects_complete_unless_draft is the constraint behind it.
UPDATE projects
SET status = 'in_progress', updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND status = 'draft' AND deleted_at IS NULL
RETURNING *;

-- name: ReevaluateProjectAutoComplete :many
-- Auto-complete rule. Returns the new status, or no row
-- when nothing should change — the common case, hence :many rather than :one
-- (which would report "no change" as pgx.ErrNoRows).
--
-- The `done < total` branch reopens a completed project that has unfinished
-- work again: pipeline re-runs un-complete jobs and API uploads add new ones,
-- neither of which re-evaluates.
--
-- TODO(soft-delete): add `AND j.deleted_at IS NULL` once jobs carry a
-- tombstone. File soft-delete must propagate to jobs.
WITH counts AS (
  SELECT count(*) AS total,
         count(*) FILTER (WHERE j.status = 'completed') AS done
  FROM jobs j
  JOIN files f ON f.id = j.file_id
  WHERE f.project_id = @project_id
)
UPDATE projects p
SET status = CASE WHEN c.total > 0 AND c.done = c.total
                  THEN 'completed'::project_status
                  ELSE 'in_progress'::project_status END,
    updated_at = now()
FROM counts c, settings_profiles sp
WHERE p.id = @project_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
  AND sp.id = p.settings_profile_id AND sp.auto_complete
  AND p.status <> 'draft'
  AND ((c.total > 0 AND c.done = c.total
        AND p.status <> 'completed' AND p.status <> 'on_hold')
    OR ((c.total = 0 OR c.done < c.total) AND p.status = 'completed'))
RETURNING p.status;

-- name: SetProjectTemplate :one
UPDATE projects SET template_id = $2, updated_at = now() WHERE id = $1 AND tenant_id = $3 AND deleted_at IS NULL RETURNING *;

-- name: MarkProjectDeleted :execrows
-- Soft-deletes a project; every read leaves it out from here on and
-- projectdelete purges it. draft_only refuses a live project in the same
-- statement, so a draft published since the caller read it stays.
UPDATE projects SET deleted_at = now(), updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND (NOT @draft_only::bool OR status = 'draft');

-- Internal: get project by ID without tenant filter (for pipeline/job use)
-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1;

-- name: ListDeletedProjects :many
-- Internal: projectdelete sweep. Every project still waiting for its purge.
SELECT id, tenant_id FROM projects WHERE deleted_at IS NOT NULL ORDER BY id;

-- name: GetDeletedProject :one
-- Internal: projectdelete. The project only while it is marked deleted.
SELECT id, tenant_id, settings_profile_id FROM projects
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NOT NULL;

-- name: PurgeDeletedProjectSegmentBatch :execrows
-- Internal: projectdelete. Up to @lim segments of a deleted project, with
-- their translations, history, holds, comments and QA results by cascade.
DELETE FROM segments
WHERE id = ANY(ARRAY(
    SELECT s.id FROM files f
    JOIN projects p ON p.id = f.project_id AND p.deleted_at IS NOT NULL
    JOIN segments s ON s.file_id = f.id
    WHERE f.project_id = @project_id
    LIMIT @lim
));

-- name: PurgeDeletedProjectFileBatch :execrows
-- Internal: projectdelete. Up to @lim files of a deleted project, run after its
-- segments are gone, with their jobs and the jobs' rows by cascade.
DELETE FROM files
WHERE id = ANY(ARRAY(
    SELECT f.id FROM files f
    JOIN projects p ON p.id = f.project_id AND p.deleted_at IS NOT NULL
    WHERE f.project_id = @project_id
    LIMIT @lim
));

-- name: PurgeDeletedProjectUsageBatch :execrows
-- Internal: projectdelete. Up to @lim AI usage events of a deleted project.
DELETE FROM ai_usage_events
WHERE id = ANY(ARRAY(
    SELECT e.id FROM ai_usage_events e
    JOIN projects p ON p.id = e.project_id AND p.deleted_at IS NOT NULL
    WHERE e.project_id = @project_id::bigint
    LIMIT @lim
));

-- name: PurgeDeletedProjectEventBatch :execrows
-- Internal: projectdelete. Up to @lim activity events of a deleted project.
DELETE FROM project_events
WHERE id = ANY(ARRAY(
    SELECT e.id FROM project_events e
    JOIN projects p ON p.id = e.project_id AND p.deleted_at IS NOT NULL
    WHERE e.project_id = @project_id::bigint
    LIMIT @lim
));

-- name: PurgeDeletedProject :one
-- Internal: projectdelete. The project row, once its large children are gone;
-- the rest goes by cascade. Returns the settings profile to delete with it.
DELETE FROM projects WHERE id = @id AND deleted_at IS NOT NULL
RETURNING settings_profile_id;

-- name: TryLockProjectDelete :one
-- Internal: projectdelete. The project's purge lock, held by the connection
-- for the whole run, so two purges of one project never run together.
SELECT pg_try_advisory_lock(hashtextextended('project-delete:' || @project_id::bigint, 0))::bool AS locked;

-- name: UnlockProjectDelete :one
-- Internal: projectdelete. Releases TryLockProjectDelete on the same connection.
SELECT pg_advisory_unlock(hashtextextended('project-delete:' || @project_id::bigint, 0))::bool AS unlocked;
