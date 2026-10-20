-- MT routers. Tenant-scoped, soft-deleted. Live-row queries filter
-- `deleted_at IS NULL`; GetMTRouterAny does not, because a settings profile
-- keeps pointing at a router after it is retired and its jobs must still
-- translate.

-- name: ListMTRouters :many
SELECT * FROM mt_routers
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY lower(name);

-- name: GetMTRouter :one
SELECT * FROM mt_routers
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: GetMTRouterAny :one
-- Resolves a router regardless of deleted_at (runtime + history lookups).
SELECT * FROM mt_routers
WHERE id = $1 AND tenant_id = $2;

-- name: ListMTRouterRulesForTenant :many
-- Every live router's rules in one round trip (the list endpoint).
SELECT rr.*
FROM mt_router_rules rr
JOIN mt_routers r ON r.id = rr.router_id
WHERE r.tenant_id = $1 AND r.deleted_at IS NULL
ORDER BY rr.router_id, rr.sort_order, rr.id;

-- name: ListMTRouterRules :many
-- Internal: caller resolves the router tenant-scoped first.
SELECT * FROM mt_router_rules
WHERE router_id = $1
ORDER BY sort_order, id;

-- name: CreateMTRouter :one
INSERT INTO mt_routers (tenant_id, name, default_mt_engine_id, default_ai_engine_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateMTRouter :one
UPDATE mt_routers
SET name = $3, default_mt_engine_id = $4, default_ai_engine_id = $5, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteMTRouter :exec
UPDATE mt_routers
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: DeleteMTRouterRules :exec
-- Internal: rules are replaced wholesale on every router save.
DELETE FROM mt_router_rules WHERE router_id = $1;

-- name: CreateMTRouterRule :one
INSERT INTO mt_router_rules (router_id, target_lang, mt_engine_id, ai_engine_id, sort_order)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CountProfileRefsForMTRouter :one
-- How many settings profiles (projects and templates) still point at this
-- router. HandleDelete refuses with 409 instead of silently NULLing them.
SELECT count(*)::int AS count
FROM settings_profiles sp
WHERE sp.mt_router_id = @router_id AND sp.tenant_id = @tenant_id
  AND settings_profile_live(sp.id);

-- name: CountMTRouterRefsForMTEngine :one
-- Live MT routers referencing a classic engine, as default or in a rule.
SELECT count(*)::int AS count
FROM mt_routers r
WHERE r.tenant_id = @tenant_id AND r.deleted_at IS NULL
  AND (r.default_mt_engine_id = @engine_id
       OR EXISTS (SELECT 1 FROM mt_router_rules rr WHERE rr.router_id = r.id AND rr.mt_engine_id = @engine_id));

-- name: CountMTRouterRefsForAIEngine :one
-- Live MT routers referencing an LLM translation engine, as default or in a rule.
SELECT count(*)::int AS count
FROM mt_routers r
WHERE r.tenant_id = @tenant_id AND r.deleted_at IS NULL
  AND (r.default_ai_engine_id = @engine_id
       OR EXISTS (SELECT 1 FROM mt_router_rules rr WHERE rr.router_id = r.id AND rr.ai_engine_id = @engine_id));

-- name: SetSettingsProfileMTRouter :exec
-- Points a profile at a router (NULL = no machine translation for the
-- profile). Tenant ownership of both is verified by the caller.
UPDATE settings_profiles SET mt_router_id = $2, updated_at = now() WHERE id = $1;
