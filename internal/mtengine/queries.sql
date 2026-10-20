-- MT engines are soft-deleted: live reads filter
-- deleted_at IS NULL. EnsureDeploymentMTEngine deliberately counts deleted rows
-- too, so a deployment engine the org deleted is never provisioned again.

-- name: ListMTEngines :many
SELECT * FROM mt_engines WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC;

-- name: GetMTEngine :one
SELECT * FROM mt_engines WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateMTEngine :one
-- settings COALESCEs so a caller with no options to set may pass nil rather
-- than the literal `{}` every time — the column is NOT NULL and an empty object
-- is what "no options" means.
INSERT INTO mt_engines (name, engine_type, api_key, api_url, enabled, tenant_id, token_id, settings)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(settings), '{}')::jsonb)
RETURNING *;

-- name: UpdateMTEngine :one
UPDATE mt_engines
SET name = $2, engine_type = $3, api_key = $4, api_url = $5, enabled = $6,
    token_id = $8, settings = COALESCE(sqlc.narg(settings), '{}')::jsonb, updated_at = now()
WHERE id = $1 AND tenant_id = $7 AND deleted_at IS NULL
RETURNING *;

-- name: GetMTEngineCredential :one
-- The engine plus the credential it spends, for the translate pipeline.
--
-- A configured engine reaches its provider through the key it names: the key
-- carries the secret (encrypted) and its connection carries the endpoint, both
-- owned by AI settings. A deployment engine has no key and falls back to its
-- own columns, which is why both sides are returned and the caller picks.
SELECT
    e.*,
    t.api_key_encrypted AS token_key_encrypted,
    c.base_url          AS token_base_url
FROM mt_engines e
LEFT JOIN ai_vendor_tokens t ON t.id = e.token_id
LEFT JOIN ai_connections c   ON c.id = t.connection_id
WHERE e.id = $1 AND e.tenant_id = $2 AND e.deleted_at IS NULL;

-- name: SoftDeleteMTEngine :exec
UPDATE mt_engines SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CountMTEngineReferences :one
-- Live MT routers that reference this engine, as default or in a rule.
-- Projects and templates reach engines only through routers, so this one
-- count covers both. Used by HandleDelete to 409 instead of cascading.
SELECT count(*)::int AS count
FROM mt_routers r
WHERE r.deleted_at IS NULL
  AND (r.default_mt_engine_id = @mt_engine_id::bigint
       OR EXISTS (SELECT 1 FROM mt_router_rules rr WHERE rr.router_id = r.id AND rr.mt_engine_id = @mt_engine_id::bigint));

-- name: EnsureDeploymentMTEngine :exec
-- Create a deployment-supplied engine for a tenant that has none of that type.
--
-- Runs once, when the tenant is created. Idempotent so a retried signup cannot
-- double up, NOT so it can be replayed: the row is the org's from then on, and
-- re-running this would resurrect one they deleted.
INSERT INTO mt_engines (name, engine_type, api_url, enabled, tenant_id, deployment_managed)
SELECT $1, $2, $3, true, $4, true
WHERE NOT EXISTS (
    -- ANY row of this type, whoever made it. A tenant that already has a mock
    -- engine does not need a second one, and adding ours beside theirs is how
    -- an upgrade silently doubles every tenant's engine list.
    SELECT 1 FROM mt_engines
    WHERE tenant_id = $4 AND engine_type = $2
);


-- name: ListKeylessMTEngines :many
-- Live engines of the tenant of type @provider whose key was deleted.
SELECT id, name FROM mt_engines
WHERE tenant_id = @tenant_id AND deleted_at IS NULL
  AND token_id IS NULL AND engine_type = @provider
ORDER BY name, id;

-- name: AttachTokenToKeylessMTEngine :execrows
-- Only an engine still keyless and of the key's vendor changes.
UPDATE mt_engines SET token_id = @token_id, updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND token_id IS NULL AND engine_type = @provider;

-- name: ListMTEnginesUsingToken :many
SELECT id, name FROM mt_engines
WHERE tenant_id = @tenant_id AND deleted_at IS NULL AND token_id = @token_id
ORDER BY name, id;
