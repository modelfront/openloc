-- AI engines are soft-deleted: live reads filter
-- deleted_at IS NULL, and a deleted engine keeps no provider rows.

-- name: ListAIEngines :many
SELECT * FROM ai_engines WHERE tenant_id = $1 AND deleted_at IS NULL ORDER BY created_at ASC;

-- name: GetAIEngine :one
SELECT * FROM ai_engines WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: ListAIEngineRowsForTenant :many
-- Every row for the tenant's engines (joined for tenant scoping), so the list
-- handler can assemble engines-with-rows in one pass.
SELECT r.* FROM ai_engine_rows r
JOIN ai_engines e ON e.id = r.engine_id
WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
ORDER BY r.engine_id, r.sort_order, r.id;

-- name: ListAIEngineRows :many
SELECT * FROM ai_engine_rows WHERE engine_id = $1 ORDER BY sort_order, id;

-- name: CreateAIEngine :one
INSERT INTO ai_engines (tenant_id, name, purpose) VALUES ($1, $2, $3) RETURNING *;

-- name: UpdateAIEngine :one
-- purpose is immutable after creation: flipping the role of an
-- engine that's already referenced by an MT assignment or a workflow step
-- would silently break that reference.
--
-- `enabled` is editable: it is an operational switch, not configuration.
-- A switched-off engine keeps its rows and its place in
-- every router, and is flagged there rather than removed.
UPDATE ai_engines SET name = $3, enabled = $4, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAIEngine :exec
UPDATE ai_engines SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: DeleteAIEngineRows :exec
DELETE FROM ai_engine_rows WHERE engine_id = $1;

-- name: CreateAIEngineRow :one
-- provider_id is intentionally absent: a row carries its own token/model/
-- settings. The column still exists but is dead.
-- provider is copied from the key's vendor here, so every insert keeps it.
INSERT INTO ai_engine_rows (
    engine_id, source_lang, target_langs, system_prompt_id,
    bad_output_action, sort_order, token_id, model, settings, provider
)
-- settings is jsonb NOT NULL; COALESCE so a caller that leaves it nil gets the
-- empty object rather than a not-null violation.
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE(sqlc.narg(settings)::jsonb, '{}'::jsonb),
    (SELECT v.provider FROM ai_vendor_tokens t JOIN ai_vendors v ON v.id = t.vendor_id WHERE t.id = $7))
RETURNING *;

-- name: CountAIRouterRefsForAIEngine :one
-- How many live routers reference this engine, as default or in a rule: AI
-- routers for a QE+APE engine, MT routers for a translation engine. Used by
-- HandleDelete to 409 instead of silently NULLing / cascading live routes.
SELECT (
  (SELECT count(*) FROM ai_routers ar
   WHERE ar.tenant_id = @tenant_id AND ar.deleted_at IS NULL
     AND (ar.default_engine_id = @engine_id
          OR EXISTS (SELECT 1 FROM ai_router_rules arr WHERE arr.router_id = ar.id AND arr.engine_id = @engine_id)))
  +
  (SELECT count(*) FROM mt_routers mr
   WHERE mr.tenant_id = @tenant_id AND mr.deleted_at IS NULL
     AND (mr.default_ai_engine_id = @engine_id
          OR EXISTS (SELECT 1 FROM mt_router_rules mrr WHERE mrr.router_id = mr.id AND mrr.ai_engine_id = @engine_id)))
)::int AS count;

-- name: ListKeylessAIEngineRows :many
-- Live engines of the tenant whose row lost its key and whose vendor is
-- @provider: the engines a new key of that vendor can fix.
SELECT e.id AS engine_id, e.name, e.purpose, r.id AS row_id, r.model, r.settings
FROM ai_engines e
JOIN ai_engine_rows r ON r.engine_id = e.id
WHERE e.tenant_id = @tenant_id AND e.deleted_at IS NULL
  AND r.token_id IS NULL AND r.provider = @provider
ORDER BY e.name, e.id;

-- name: AttachTokenToKeylessAIEngine :execrows
-- Gives a keyless row of the engine the key. Only rows still keyless and of the
-- key's vendor change, so a stale request updates nothing.
UPDATE ai_engine_rows r SET token_id = @token_id
FROM ai_engines e
WHERE r.engine_id = e.id AND e.id = @engine_id AND e.tenant_id = @tenant_id
  AND e.deleted_at IS NULL AND r.token_id IS NULL AND r.provider = @provider;

-- name: ListAIEnginesUsingToken :many
SELECT DISTINCT e.id, e.name, e.purpose
FROM ai_engines e
JOIN ai_engine_rows r ON r.engine_id = e.id
WHERE e.tenant_id = @tenant_id AND e.deleted_at IS NULL AND r.token_id = @token_id
ORDER BY e.name, e.id;
