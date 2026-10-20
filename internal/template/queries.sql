-- name: ListTemplates :many
-- Internal: unfiltered, no pagination. Used by seed and worker-side code that
-- needs every row in the tenant. UI uses ListTemplatesFiltered.
SELECT * FROM templates WHERE tenant_id = $1 ORDER BY created_at DESC;

-- name: ListTemplatesFiltered :many
-- Paginated + searchable template list for the UI (the New project picker).
-- Sort by id DESC for stable cursor pagination (newest-created first).
-- Caller passes limit+1 to detect has_more in the handler.
SELECT * FROM templates
WHERE tenant_id = @tenant_id
  AND (@cursor::bigint = 0 OR id < @cursor)
  AND (sqlc.narg('q')::text IS NULL OR name ILIKE '%' || sqlc.narg('q')::text || '%')
ORDER BY id DESC
LIMIT @lim;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = $1 AND tenant_id = $2;

-- name: CreateTemplate :one
-- A template's project name starts as the template's own name unless the
-- caller gives one, so every creator (API, duplicate, seed) agrees.
INSERT INTO templates (name, tenant_id, settings_profile_id, project_name, source_language, target_languages, customer_id, domain)
VALUES (@name, @tenant_id, @settings_profile_id, COALESCE(NULLIF(TRIM(sqlc.narg('project_name')::text), ''), @name), @source_language, COALESCE(@target_languages::text[], '{}'::text[]), @customer_id, @domain)
RETURNING *;

-- name: UpdateTemplate :one
UPDATE templates
SET name = @name, project_name = @project_name, source_language = @source_language,
    target_languages = COALESCE(@target_languages::text[], '{}'::text[]),
    customer_id = @customer_id, domain = @domain, updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id
RETURNING *;

-- name: DeleteTemplate :exec
DELETE FROM templates WHERE id = $1 AND tenant_id = $2;

-- Internal: get template by ID without tenant filter (for pipeline use)
-- name: GetTemplateByID :one
SELECT * FROM templates WHERE id = $1;

-- name: CountProjectsFromTemplate :one
-- Projects created from a template. They copied its configuration, so an edit
-- or delete of the template does not change them.
SELECT count(*)::bigint FROM projects
WHERE template_id = sqlc.narg('template_id') AND tenant_id = @tenant_id AND deleted_at IS NULL;
