-- Per-user language pair qualification.
-- Tenant isolation goes through user_id → memberships(tenant_id). Every query scopes through the membership so
-- a multi-org linguist's pairs are visible/editable only in the org being viewed.

-- name: ListUserLanguagePairs :many
-- List all pairs for a user, scoped to an org via the membership.
-- Ordered by row_group first so the client can reconstruct editor rows in order.
SELECT ulp.user_id, ulp.source_lang, ulp.target_lang, ulp.row_group, ulp.created_at
FROM user_language_pairs ulp
JOIN memberships m ON m.user_id = ulp.user_id AND m.tenant_id = $2
WHERE ulp.user_id = $1
ORDER BY ulp.row_group, ulp.source_lang, ulp.target_lang;

-- name: ListUsersForLanguagePair :many
-- List members of an org qualified for a given source→target pair.
-- Used by the workflow assignment dropdown filter.
-- Filters out soft-deleted / inactive users. Role comes from the membership.
SELECT u.id, u.email, u.name, m.role
FROM users u
JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $1
JOIN user_language_pairs ulp ON ulp.user_id = u.id
WHERE u.is_active = true
  AND u.deactivated_at IS NULL
  AND ulp.source_lang = $2
  AND ulp.target_lang = $3
ORDER BY u.name;

-- name: DeleteUserLanguagePairs :exec
-- Used by the PUT replace-semantics handler. Always paired with bulk insert
-- inside a transaction so the user's pair set is atomically replaced.
-- Org-scoped via the membership subquery — won't touch a user's pairs unless
-- they're a member of the given tenant.
DELETE FROM user_language_pairs
WHERE user_language_pairs.user_id = $1
  AND user_language_pairs.user_id IN (SELECT memberships.user_id FROM memberships WHERE memberships.tenant_id = $2);

-- name: GetUserInTenant :one
-- Org-scoped existence check. Returns ErrNoRows if the user isn't an active
-- member of the tenant — used by HandleReplace to surface a clean 404 instead
-- of silently no-op'ing on a foreign user_id.
SELECT u.id FROM users u
JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $2
WHERE u.id = $1 AND u.is_active = true AND u.deactivated_at IS NULL;

-- name: InsertUserLanguagePair :exec
-- Single-pair insert. Bulk insert is done by the handler in a loop inside
-- a transaction (sqlc's batch insert support is awkward for our setup).
-- Org-scoped via the membership subquery — the INSERT silently no-ops if the
-- user isn't a member of tenant_id (defense in depth).
INSERT INTO user_language_pairs (user_id, source_lang, target_lang, row_group)
SELECT sqlc.arg(user_id), sqlc.arg(source_lang), sqlc.arg(target_lang), sqlc.arg(row_group)
FROM memberships
WHERE memberships.user_id = sqlc.arg(user_id) AND memberships.tenant_id = sqlc.arg(tenant_id)
ON CONFLICT (user_id, source_lang, target_lang) DO NOTHING;
