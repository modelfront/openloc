-- name: CreateSegment :one
INSERT INTO segments (file_id, source_text, segment_key, position, context)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSegmentsByFile :many
-- Internal: the file's shared segments, as imported, for server-side work over
-- the whole file. Rows a split or join made for one language are left out.
SELECT * FROM segments WHERE file_id = $1 AND target_language IS NULL ORDER BY position;

-- name: ListSegmentsByFileTenant :many
-- Tenant-scoped: returns the file's shared segments, as imported, only if the
-- file belongs to caller's tenant, at most @row_limit of them.
SELECT s.* FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.file_id = @file_id AND s.target_language IS NULL
  AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
ORDER BY s.position
LIMIT @row_limit::int;

-- name: GetSegment :one
-- Internal: used by pipeline. For user-facing handlers, use GetSegmentTenant.
SELECT * FROM segments WHERE id = $1;

-- name: GetSegmentTenant :one
-- Tenant-scoped: returns segment only if it belongs to the caller's tenant.
SELECT s.* FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: GetSegmentInFileTenant :one
-- File- and tenant-scoped: returns the segment only if it belongs to the file
-- in the route, so file-level access to one file does not open another's segments.
-- Shared segments only, like ListSegmentsByFileTenant.
SELECT s.* FROM segments s
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE s.id = @id AND s.file_id = @file_id AND s.target_language IS NULL AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: DeleteSegmentsByFile :exec
-- Internal: clears a draft file's segments so publish can store a fresh parse.
-- The caller has already resolved the file within the tenant; a draft file has
-- no jobs, so only its own translations (none yet) cascade.
DELETE FROM segments WHERE file_id = $1;
