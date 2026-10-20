-- AI routers. Tenant-scoped, soft-deleted. Live-row
-- queries filter `deleted_at IS NULL`; GetAIRouterAny does not, because a
-- profile step row keeps pointing at a router after it is retired, and the
-- running job must still resolve its engine.

-- name: ListAIRouters :many
SELECT * FROM ai_routers
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY lower(name);

-- name: GetAIRouter :one
SELECT * FROM ai_routers
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: GetAIRouterAny :one
-- Resolves a router regardless of deleted_at (runtime + history lookups).
SELECT * FROM ai_routers
WHERE id = $1 AND tenant_id = $2;

-- name: ListAIRouterRulesForTenant :many
-- Every live router's rules in one round trip (the list endpoint).
SELECT rr.*
FROM ai_router_rules rr
JOIN ai_routers r ON r.id = rr.router_id
WHERE r.tenant_id = $1 AND r.deleted_at IS NULL
ORDER BY rr.router_id, rr.sort_order, rr.id;

-- name: ListAIRouterRules :many
-- Internal: caller resolves the router tenant-scoped first.
SELECT * FROM ai_router_rules
WHERE router_id = $1
ORDER BY sort_order, id;

-- name: CreateAIRouter :one
INSERT INTO ai_routers (tenant_id, name, default_engine_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateAIRouter :one
UPDATE ai_routers
SET name = $3, default_engine_id = $4, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAIRouter :exec
UPDATE ai_routers
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: DeleteAIRouterRules :exec
-- Internal: rules are replaced wholesale on every router save.
DELETE FROM ai_router_rules WHERE router_id = $1;

-- name: CreateAIRouterRule :one
INSERT INTO ai_router_rules (router_id, target_lang, engine_id, sort_order)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountStepRefsForAIRouter :one
-- How many steps still point at this router: live library definitions plus
-- profile snapshots (project and template settings). HandleDelete refuses
-- with 409 instead of silently NULLing them.
SELECT (
    (SELECT count(*) FROM step_definitions sd
     WHERE sd.ai_router_id = @router_id AND sd.tenant_id = @tenant_id AND sd.deleted_at IS NULL)
  + (SELECT count(*) FROM workflow_steps ws
     JOIN settings_profiles sp ON sp.id = ws.settings_profile_id
     WHERE ws.ai_router_id = @router_id AND sp.tenant_id = @tenant_id
       AND settings_profile_live(sp.id))
)::int AS count;

-- name: ListStepDefinitionsUsingAIRouter :many
-- Live library definitions that run a router.
SELECT id, name FROM step_definitions
WHERE ai_router_id = $1 AND tenant_id = $2 AND deleted_at IS NULL
ORDER BY lower(name);
