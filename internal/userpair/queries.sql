-- Per-org language pair qualification. A pair belongs to a membership
-- (tenant_id, user_id): each org sets and reads only its own pairs for a person.

-- name: ListUserLanguagePairs :many
-- List a member's pairs in one org.
-- Ordered by row_group first so the client can reconstruct editor rows in order.
SELECT ulp.user_id, ulp.source_lang, ulp.target_lang, ulp.row_group, ulp.created_at
FROM user_language_pairs ulp
WHERE ulp.user_id = $1 AND ulp.tenant_id = $2
ORDER BY ulp.row_group, ulp.source_lang, ulp.target_lang;

-- name: ListLanguagePairsForMembers :many
-- The pairs of several members of one org at once (one page of the Members
-- tab). Ordered like ListUserLanguagePairs.
SELECT ulp.user_id, ulp.source_lang, ulp.target_lang, ulp.row_group
FROM user_language_pairs ulp
WHERE ulp.tenant_id = @tenant_id AND ulp.user_id = ANY(@user_ids::bigint[])
ORDER BY ulp.user_id, ulp.row_group, ulp.source_lang, ulp.target_lang;

-- name: ListUsersForLanguagePair :many
-- List members of an org qualified for a given source→target pair in that org.
-- Used by the workflow assignment dropdown filter.
-- Filters out soft-deleted / inactive users. Role comes from the membership.
SELECT u.id, u.email, u.name, m.role
FROM users u
JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $1
JOIN user_language_pairs ulp ON ulp.user_id = u.id AND ulp.tenant_id = m.tenant_id
WHERE u.is_active = true
  AND u.deactivated_at IS NULL
  AND ulp.source_lang = $2
  AND ulp.target_lang = $3
ORDER BY u.name;

-- name: DeleteUserLanguagePairs :exec
-- Used by the PUT replace-semantics handler. Always paired with bulk insert
-- inside a transaction so the member's pair set is atomically replaced.
-- Touches only the pairs this org set.
DELETE FROM user_language_pairs
WHERE user_language_pairs.user_id = $1 AND user_language_pairs.tenant_id = $2;

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
-- Org-scoped via the membership: the INSERT silently no-ops if the user isn't
-- a member of tenant_id (defense in depth).
INSERT INTO user_language_pairs (tenant_id, user_id, source_lang, target_lang, row_group)
SELECT memberships.tenant_id, memberships.user_id, sqlc.arg(source_lang), sqlc.arg(target_lang), sqlc.arg(row_group)
FROM memberships
WHERE memberships.user_id = sqlc.arg(user_id) AND memberships.tenant_id = sqlc.arg(tenant_id)
ON CONFLICT (tenant_id, user_id, source_lang, target_lang) DO NOTHING;
