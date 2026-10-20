-- name: CreateFile :one
-- inline_html is nil-keeps-default (on): a struct literal that omits it must
-- not parse a file as plain text. xlsx_layout is NULL for every file but an
-- Excel workbook the uploader laid out (parser.XLSXLayout).
INSERT INTO files (project_id, name, format, storage_path, segment_count, inline_html, xlsx_layout)
VALUES ($1, $2, $3, $4, $5, COALESCE(sqlc.narg('inline_html')::bool, true), sqlc.narg('xlsx_layout')::jsonb)
RETURNING *;

-- name: LockFileTenant :one
-- Locks the file row for the rest of the transaction. The layout change and
-- publish both take it, so one waits for the other. Tenant-scoped through the
-- file's project.
SELECT f.* FROM files f
JOIN projects p ON p.id = f.project_id
WHERE f.id = @id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
FOR UPDATE OF f;

-- name: FileLayoutEditable :one
-- A file's layout may change while its project is a draft and it has no jobs
-- yet; a publish that failed part-way leaves a draft with some files under
-- jobs. Run after LockFileTenant, so it sees the jobs a publish committed.
SELECT COALESCE(p.status = 'draft' AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.file_id = f.id), false)::bool AS editable
FROM files f
JOIN projects p ON p.id = f.project_id
WHERE f.id = @id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: FileHasJobs :one
-- Run after LockFileTenant: a publish that waited on the lock sees the jobs
-- the publish before it committed. Tenant-scoped through the file's project.
SELECT EXISTS (
    SELECT 1 FROM jobs j
    JOIN files f ON f.id = j.file_id
    JOIN projects p ON p.id = f.project_id
    WHERE j.file_id = @id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
)::bool AS has_jobs;

-- name: UpdateFileXLSXLayout :one
-- Tenant-scoped through the file's project.
UPDATE files f
SET xlsx_layout = @xlsx_layout::jsonb, updated_at = now()
FROM projects p
WHERE f.id = @id AND p.id = f.project_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL
RETURNING f.*;

-- name: GetFile :one
-- Internal: used by pipeline. For user-facing handlers, use GetFileTenant.
SELECT * FROM files WHERE id = $1;

-- name: GetFileTenant :one
-- Tenant-scoped: returns file only if it belongs to the caller's tenant.
SELECT f.* FROM files f
JOIN projects p ON p.id = f.project_id
WHERE f.id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: ListFilesByProject :many
-- Used internally (pipeline, export). For paginated API responses, use ListFilesPaginated.
SELECT * FROM files WHERE project_id = $1 ORDER BY created_at DESC;

-- name: ListFilesPaginated :many
SELECT * FROM files
WHERE project_id = @project_id AND id > @cursor
ORDER BY id
LIMIT @page_limit;

-- name: ListFilesWithoutJobs :many
-- Files uploaded while the project was a draft: stored and segmented, but
-- their translations and jobs are created at publish.
SELECT f.* FROM files f
JOIN projects p ON p.id = f.project_id
WHERE f.project_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.file_id = f.id)
ORDER BY f.created_at, f.id;

-- name: CountFilesByProject :one
SELECT count(*) FROM files WHERE project_id = $1;

-- name: UpdateFileSegmentCount :exec
UPDATE files SET segment_count = $2, updated_at = now() WHERE id = $1;

-- name: RenameFile :one
UPDATE files
SET name = $2, original_name = COALESCE(original_name, name), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RenameFileTenant :one
-- Tenant-scoped rename: only if file belongs to caller's tenant.
UPDATE files
SET name = @name, original_name = COALESCE(original_name, name), updated_at = now()
WHERE files.id = @id AND project_id IN (SELECT projects.id FROM projects WHERE projects.tenant_id = @tenant_id AND projects.deleted_at IS NULL)
RETURNING *;

-- name: DeleteFile :exec
DELETE FROM files WHERE id = $1;

-- name: DeleteFileTenant :exec
-- Tenant-scoped delete: only if file belongs to caller's tenant.
DELETE FROM files
WHERE files.id = $1 AND project_id IN (SELECT projects.id FROM projects WHERE projects.tenant_id = $2 AND projects.deleted_at IS NULL);

-- name: ListTranslationsByFileForReimport :many
-- Returns the minimal projection the XLIFF re-import diff needs:
-- segment_key (the match key against incoming trans-unit IDs), translation
-- id/locked/status + the currently-active rendered target (used to detect
-- "no change" against the incoming target). Tenant scoping is enforced by
-- the caller via GetFileTenant before invoking this. held_by_other: a user
-- other than @actor_id (the importer) has the row open.
SELECT t.id AS translation_id,
       s.id AS segment_id,
       s.segment_key AS segment_key,
       t.locked AS locked,
       t.status AS status,
       t.active_text AS active_text,
       t.active_rich_text AS active_rich_text,
       translation_held_by_other(t.id, sqlc.narg('actor_id')::bigint) AS held_by_other
FROM translations t
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = @file_id
  AND t.target_language = @target_language
ORDER BY s.position;

-- name: UpdateTranslationFromReimport :execrows
-- XLIFF re-import: stamps a translation with the incoming target. Treats the
-- incoming text as a manual edit from outside OpenLoc, so it writes the
-- active_* columns but sets status to 'needs_review' rather than
-- 'approved' — a re-import is a fresh edit that PMs should explicitly
-- re-confirm. The `file_import` provenance lives in
-- translation_history (the reimport handler emits a `file_import`
-- automation event).
-- edited_by records the admin who triggered the re-import (audit trail —
-- the actual text came from outside OpenLoc, but the operation was
-- performed by a known user).
-- A row a user other than @edited_by holds is not written: zero rows.
UPDATE translations
SET step_edited      = step_edited OR active_text IS DISTINCT FROM @human_text,
    qe_note          = CASE WHEN active_text IS DISTINCT FROM @human_text THEN NULL ELSE qe_note END,
    active_text      = @human_text,
    active_rich_text = @human_rich_text,
    status           = 'needs_review',
    edited_by        = @edited_by,
    updated_at       = now()
WHERE id = @id
  AND NOT translation_held_by_other(translations.id, @edited_by);

-- name: ImportBilingualTargets :execrows
-- XLSX bilingual import, one target language in one statement: stamps each
-- (segment, @target_language) translation with the imported text as
-- pre-translated (not approved — translators still review), sets origin
-- file_import, and records the file_import history row. An empty rich text
-- means the cell has no inline tags. Internal: @segment_ids are the segments
-- the upload just created, in its transaction.
WITH input AS (
    SELECT unnest(@segment_ids::bigint[]) AS segment_id,
           unnest(@texts::text[])        AS text,
           unnest(@rich_texts::text[])   AS rich_text
), stamped AS (
    UPDATE translations t
    SET active_text      = i.text,
        active_rich_text = NULLIF(i.rich_text, ''),
        status           = 'needs_review',
        origin           = 'file_import',
        tm_match_percent = NULL,
        updated_at       = now()
    FROM input i
    WHERE t.segment_id = i.segment_id AND t.target_language = @target_language
    RETURNING t.id, i.text, NULLIF(i.rich_text, '') AS rich_text
)
INSERT INTO translation_history (translation_id, kind, text, rich_text, created_by, details)
SELECT stamped.id, 'file_import', stamped.text, stamped.rich_text, @created_by::bigint, '{}'::jsonb
FROM stamped;
