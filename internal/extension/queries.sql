-- name: ListExtensions :many
SELECT * FROM extensions
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY name, ext_id;

-- name: GetExtension :one
SELECT * FROM extensions
WHERE tenant_id = $1 AND ext_id = $2 AND deleted_at IS NULL;

-- name: GetExtensionIcon :one
SELECT icon FROM extensions
WHERE tenant_id = $1 AND ext_id = $2 AND deleted_at IS NULL;

-- name: UpsertExtension :one
-- Install, or replace the live extension with the same ext_id.
INSERT INTO extensions (tenant_id, ext_id, name, version, api_version, capabilities, manifest, storage_key, sha256, created_by, icon)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (tenant_id, ext_id) WHERE deleted_at IS NULL DO UPDATE
SET name = EXCLUDED.name, version = EXCLUDED.version, api_version = EXCLUDED.api_version,
    capabilities = EXCLUDED.capabilities, manifest = EXCLUDED.manifest,
    storage_key = EXCLUDED.storage_key, sha256 = EXCLUDED.sha256, icon = EXCLUDED.icon, updated_at = now()
RETURNING *;

-- name: RemoveExtension :one
-- Soft-deletes the extension and hard-deletes its vendor row in one
-- statement; the row's keys and endpoints cascade with it. Engine rows on
-- those keys keep their place with token_id set to NULL, so they read as
-- having no key. Returns the number of extensions removed.
WITH removed AS (
    UPDATE extensions SET deleted_at = now(), updated_at = now()
    WHERE extensions.tenant_id = $1 AND ext_id = $2 AND deleted_at IS NULL
    RETURNING extensions.tenant_id, ext_id
), vendor AS (
    DELETE FROM ai_vendors v
    USING removed r
    WHERE v.tenant_id = r.tenant_id AND v.provider = 'ext:' || r.ext_id
    RETURNING v.id
)
SELECT count(*) FROM removed;
