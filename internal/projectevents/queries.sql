-- Project audit log (project_events). Event taxonomy + emit helpers live in
-- internal/projectevents/events.go.

-- name: InsertProjectEvent :one
INSERT INTO project_events (project_id, tenant_id, event_type, actor_id, file_id, job_id, details)
VALUES ($1, $2, $3, $4, $5, $6, COALESCE(sqlc.narg(details)::jsonb, '{}'::jsonb))
RETURNING id;

-- name: CompleteProjectCreatedEvent :execrows
-- Fills a draft's project.created with the configuration it is published with.
-- The actor and the snapshots taken when the draft started (actor_name, via)
-- stay; every other key is replaced, so a template dropped in the wizard does
-- not linger.
UPDATE project_events
SET details = sqlc.arg(details)::jsonb
           || jsonb_strip_nulls(jsonb_build_object('actor_name', details->'actor_name', 'via', details->'via'))
WHERE project_id = sqlc.arg(project_id) AND tenant_id = sqlc.arg(tenant_id)
  AND event_type = 'project.created';

-- name: ListProjectEvents :many
-- Newest-first cursor pagination keyed on id (identity column ⇒ id order ==
-- insert order). cursor=0 means "first page". Tenant scoping is on the
-- denormalized column; callers must still 404 unknown projects via GetProject.
-- Optional filters: actor_id=0 means any user; event_prefix='' means any type
-- (a prefix like 'file.' filters a category, a full type like 'file.uploaded'
-- filters exactly — LIKE prefix% covers both).
SELECT pe.id, pe.event_type, pe.actor_id,
       u.name AS actor_name, u.email AS actor_email,
       pe.file_id, pe.job_id, pe.details, pe.created_at
FROM project_events pe
LEFT JOIN users u ON u.id = pe.actor_id
WHERE pe.project_id = $1 AND pe.tenant_id = $2
  AND (@cursor::bigint = 0 OR pe.id < @cursor)
  AND (@actor_id::bigint = 0 OR pe.actor_id = @actor_id)
  AND (@event_prefix::text = '' OR pe.event_type LIKE @event_prefix || '%')
ORDER BY pe.id DESC
LIMIT $3;
