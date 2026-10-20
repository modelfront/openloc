-- Step definitions library. Tenant-scoped, soft-deleted.
-- Live-row queries filter `deleted_at IS NULL`; GetStepDefinitionAny does not,
-- because profile step rows keep a provenance pointer to a definition even
-- after it is retired from the library. Built-in definitions (system_key set)
-- are not library rows: the library queries skip them, so they cannot be
-- listed, composed, edited or deleted.

-- name: ListStepDefinitions :many
SELECT sd.*,
       rt.name AS ai_router_name,
       (SELECT count(DISTINCT wds.workflow_id)
        FROM workflow_definition_steps wds
        JOIN workflows w ON w.id = wds.workflow_id
        WHERE wds.step_definition_id = sd.id AND w.deleted_at IS NULL)::bigint AS workflow_count
FROM step_definitions sd
LEFT JOIN ai_routers rt ON rt.id = sd.ai_router_id
WHERE sd.tenant_id = $1 AND sd.deleted_at IS NULL AND sd.system_key IS NULL
ORDER BY lower(sd.name);

-- name: GetStepDefinition :one
SELECT * FROM step_definitions
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL;

-- name: GetApplicableStepDefinition :one
-- A live definition a project or template step may snapshot: a library row or
-- a built-in one.
SELECT * FROM step_definitions
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: GetSystemStepDefinition :one
SELECT * FROM step_definitions
WHERE tenant_id = $1 AND system_key = $2 AND deleted_at IS NULL;

-- name: EnsureSystemStepDefinition :exec
-- Idempotent insert of a built-in definition at tenant provisioning.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required, system_key)
VALUES ($1, $2, $3, 'linguist', $4)
ON CONFLICT (tenant_id, system_key) WHERE system_key IS NOT NULL AND deleted_at IS NULL DO NOTHING;

-- name: LockStepDefinition :one
-- Row-locks a live definition (FOR UPDATE) so a delete and a concurrent
-- workflow-compose serialize on it: whichever commits second observes the
-- other's effect (a removed workflow, or a now-deleted definition) instead of
-- racing a check-then-act. Callers run it inside a transaction.
SELECT * FROM step_definitions
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL
FOR UPDATE;

-- name: GetStepDefinitionAny :one
-- Resolves a definition regardless of deleted_at (provenance lookups).
SELECT * FROM step_definitions
WHERE id = $1 AND tenant_id = $2;

-- name: GetStepDefinitionByName :one
SELECT * FROM step_definitions
WHERE tenant_id = $1 AND lower(name) = lower(sqlc.arg(name)) AND deleted_at IS NULL AND system_key IS NULL;

-- name: CreateStepDefinition :one
INSERT INTO step_definitions (tenant_id, name, step_type, role_required, ai_router_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: EnsureStepDefinition :exec
-- Idempotent insert for the tenant defaults (Translation / Review). The
-- partial unique index on library names is the conflict target, so a tenant
-- that renamed or deleted a default is left alone.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required)
VALUES ($1, $2, 'human', 'linguist')
ON CONFLICT (tenant_id, lower(name)) WHERE deleted_at IS NULL AND system_key IS NULL DO NOTHING;

-- name: UpdateStepDefinition :one
UPDATE step_definitions
SET name = $3, step_type = $4, role_required = $5, ai_router_id = $6, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL
RETURNING *;

-- name: SoftDeleteStepDefinition :exec
UPDATE step_definitions
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL;

-- name: ListWorkflowsUsingStepDefinition :many
-- Live library workflows that reference a definition — surfaced in the 409
-- when a PM tries to delete a step that is still composed into a workflow.
SELECT DISTINCT w.id, w.name
FROM workflow_definition_steps wds
JOIN workflows w ON w.id = wds.workflow_id
WHERE wds.step_definition_id = $1 AND w.tenant_id = $2 AND w.deleted_at IS NULL
ORDER BY w.name;
