-- name: CreateTenant :one
INSERT INTO tenants (name, slug) VALUES ($1, $2) RETURNING *;

-- name: CreatePendingTenant :one
-- Self-serve org signup: create a tenant awaiting operator approval,
-- with its domain bound up-front (drives domain auto-join once approved).
-- A taken slug returns no row instead of an error, so the caller's
-- transaction stays usable for the next slug candidate.
INSERT INTO tenants (name, slug, domain, status)
VALUES ($1, $2, $3, 'pending')
ON CONFLICT (slug) DO NOTHING
RETURNING *;

-- name: CreateApprovedTenant :one
-- Platform-admin org provisioning: an operator creates the org
-- deliberately, so it starts approved — the pending/approval loop only gates
-- self-serve signups.
-- A taken slug returns no row, as in CreatePendingTenant.
INSERT INTO tenants (name, slug, domain, status)
VALUES ($1, $2, $3, 'approved')
ON CONFLICT (slug) DO NOTHING
RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: GetTenantBySlug :one
SELECT * FROM tenants WHERE slug = $1;

-- name: UpdateTenantProfile :one
UPDATE tenants SET name = $2, slug = $3, domain = $4 WHERE id = $1 RETURNING *;

-- name: GetTenantByDomainOpenReg :one
-- Domain auto-join. Only APPROVED orgs absorb same-domain colleagues:
-- a pending/rejected signup must not silently pull in the rest of the domain
-- before an operator has approved it.
SELECT * FROM tenants WHERE domain = $1 AND open_registration = true AND status = 'approved';

-- name: GetTenantByDomain :one
-- Any tenant that already owns a domain, regardless of status. Guards self-serve
-- signup: a second person from an already-claimed domain must be invited /
-- wait for approval rather than create a duplicate org.
SELECT * FROM tenants WHERE domain = $1;

-- name: GetTenantStatus :one
-- Per-request pending-gate. auth.Middleware calls this so approval
-- takes effect on the next request without a re-login (a JWT claim would go
-- stale for the token's 24h lifetime).
-- Also returns kind: the demo organization is read-only.
SELECT status, kind FROM tenants WHERE id = $1;

-- name: SetTenantStatus :one
-- An organization being deleted keeps its status: its delete is under way.
UPDATE tenants SET status = $2 WHERE id = $1 AND status <> 'deleting' RETURNING *;

-- name: SetTenantDomain :exec
UPDATE tenants SET domain = $2 WHERE id = $1;

-- name: GetTenantOwner :one
-- The identity that owns a tenant (the signup creator). Used to notify them on
-- approve/reject. Earliest owner if somehow multiple.
SELECT u.id, u.email, u.name
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.tenant_id = $1 AND m.role = 'owner'
ORDER BY m.created_at
LIMIT 1;

-- name: ListTenantsForPlatform :many
-- Operator org list: every organization with its status + owner,
-- pending first (they need action), then newest. LEFT JOIN owner so an org
-- without one still appears (owner_email/name are then NULL).
SELECT t.id AS tenant_id, t.name, t.slug, t.domain, t.status, t.created_at,
       u.email AS owner_email, u.name AS owner_name
FROM tenants t
LEFT JOIN LATERAL (
    SELECT m.user_id FROM memberships m
    WHERE m.tenant_id = t.id AND m.role = 'owner'
    ORDER BY m.created_at
    LIMIT 1
) owner ON true
LEFT JOIN users u ON u.id = owner.user_id
-- The demo organization is built and rebuilt by the server, not managed here.
WHERE t.kind = 'customer' AND t.status <> 'deleting'
ORDER BY (t.status = 'pending') DESC, t.created_at DESC;

-- name: UpdateTenantOpenRegistration :exec
UPDATE tenants SET open_registration = $2 WHERE id = $1;

-- name: CreateUser :one
-- Creates a GLOBAL identity. Org membership + role are established
-- separately via CreateMembership. last_tenant_id starts NULL and is set to
-- the org the user most recently entered (SetLastTenant), used only to default
-- the org picker.
INSERT INTO users (email, password_hash, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: SetLastTenant :exec
-- Records the org a user most recently entered (defaults the org picker).
UPDATE users SET last_tenant_id = $2 WHERE id = $1;

-- name: CreateMembership :exec
-- Idempotent materialize of a (user, tenant, role) membership. Safe to call on
-- every login for invite/domain auto-join paths — ON CONFLICT keeps the
-- existing role rather than overwriting it.
INSERT INTO memberships (user_id, tenant_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, tenant_id) DO NOTHING;

-- name: GetMembership :one
-- Resolves a user's role in a specific org. ErrNoRows = not a member (used to
-- 404 the org-select endpoint for a tenant the user can't enter).
SELECT * FROM memberships WHERE user_id = $1 AND tenant_id = $2;

-- name: ListMembershipsWithTenant :many
-- The org picker: every org this identity can enter, with the user's role. An
-- organization being deleted is gone for its members already.
SELECT m.tenant_id, t.name AS tenant_name, t.slug AS tenant_slug, m.role
FROM memberships m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.user_id = $1 AND t.status <> 'deleting'
ORDER BY t.name;

-- name: DeleteMembership :exec
-- Remove a user from ONE org (per-org "remove member"), leaving the global
-- identity and any other memberships intact.
DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2;

-- name: UpdateMembershipRole :exec
UPDATE memberships SET role = $3 WHERE user_id = $1 AND tenant_id = $2;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetActiveUser :one
-- Used by auth middleware: a deactivated user's JWT must instantly 401.
-- Returns ErrNoRows if the
-- user doesn't exist OR has been deactivated.
SELECT * FROM users WHERE id = $1 AND is_active = true;

-- name: DeactivateUser :exec
-- Soft-delete: keeps identity and group memberships, so `edited_by`
-- references on translations stay valid.
UPDATE users SET is_active = false, deactivated_at = now() WHERE id = $1;

-- name: ReactivateUser :one
UPDATE users SET is_active = true, deactivated_at = NULL WHERE id = $1 RETURNING *;

-- name: UpdateUserProfile :one
-- The email is not a profile field: nothing may change it.
UPDATE users SET name = $2 WHERE id = $1 RETURNING *;

-- name: CountAdminsInTenant :one
-- Counts ACTIVE admins/owners in the org (membership-based). Last-admin
-- guard must not count deactivated users (otherwise demoting/deactivating the
-- last truly-active admin would be silently allowed).
SELECT count(*) FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.tenant_id = $1 AND u.is_active = true AND m.role IN ('admin', 'owner');

-- name: ListMembersByTenant :many
-- Members of an org (membership-based). Role comes from the membership,
-- not the user row. Default: active users only; pass include_inactive=true to
-- also return deactivated users (admin "deactivated members" view).
SELECT u.id, m.tenant_id, u.email, u.name, m.role, u.created_at, u.is_active, u.deactivated_at
FROM users u
JOIN memberships m ON m.user_id = u.id
WHERE m.tenant_id = $1
  AND (sqlc.arg(include_inactive)::bool OR u.is_active = true)
ORDER BY u.created_at;

-- name: DeleteTenant :exec
-- Hard delete — the last step of an organization's delete (tenantdelete).
-- Cascades through every FK with ON DELETE CASCADE.
DELETE FROM tenants WHERE id = $1;

-- name: MarkTenantDeleting :execrows
-- The owner closes the organization: from now on no member can use it, and
-- tenantdelete removes its data in the background. Its domain is freed at
-- once, so a new organization for it can be created meanwhile.
UPDATE tenants SET status = 'deleting', domain = NULL WHERE id = $1;

-- name: ListDeletingTenants :many
-- Internal: the tenant-delete sweep.
SELECT id FROM tenants WHERE status = 'deleting' ORDER BY id;

-- name: ListTenantProjectIDsPage :many
-- Internal: tenantdelete. A page of the tenant's project ids after @after_id.
SELECT id FROM projects
WHERE tenant_id = @tenant_id AND id > @after_id
ORDER BY id
LIMIT @lim;

-- name: DeleteProjectFileBatch :execrows
-- Internal: tenantdelete. One file of the project, with its segments,
-- translations and their history by cascade. The caller took the project
-- from ListTenantProjectIDsPage of the tenant being deleted.
DELETE FROM files
WHERE id = (SELECT f.id FROM files f WHERE f.project_id = sqlc.arg(project_id)::bigint LIMIT 1);

-- name: DeleteTenantTMEntryBatch :execrows
-- Internal: tenantdelete. Up to 5000 translation memory entries of the tenant.
-- The walk starts from the tenant's TMs and reads each one's entries through
-- the TM index, so a step reads the tenant's rows only. Concepts walk the
-- same way.
DELETE FROM translation_memory
WHERE id = ANY(ARRAY(
    SELECT e.id FROM tm_collections c
    CROSS JOIN LATERAL (SELECT id FROM translation_memory WHERE tm_id = c.id LIMIT 5000) e
    WHERE c.tenant_id = @tenant_id
    LIMIT 5000
));

-- name: DeleteTenantTermConceptBatch :execrows
-- Internal: tenantdelete. Up to 5000 term concepts of the tenant, live or
-- soft-deleted, with their terms by cascade.
DELETE FROM term_concept
WHERE id = ANY(ARRAY(
    SELECT c.id FROM term_bases b
    CROSS JOIN LATERAL (SELECT id FROM term_concept WHERE term_base_id = b.id LIMIT 5000) c
    WHERE b.tenant_id = @tenant_id
    LIMIT 5000
));

-- name: SetUserPassword :exec
-- Sets (or replaces) the bcrypt hash for password login. Reached only
-- via the password-reset flow — never from an invite token: accept-invite
-- creates new accounts only, so a forwarded invite link can't set a password
-- on an existing account.
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: CreatePasswordResetToken :one
-- token_hash = SHA-256 hex of the raw token (PAT pattern); plaintext is emailed
-- once and never stored.
INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetValidPasswordResetToken :one
-- Resolves a presented reset token iff it's unused, unexpired, and the user is
-- still active. ErrNoRows = flat 400 to the client (no state leakage).
SELECT prt.id, prt.user_id, u.email
FROM password_reset_tokens prt
JOIN users u ON u.id = prt.user_id
WHERE prt.token_hash = $1
  AND prt.used_at IS NULL
  AND prt.expires_at > now()
  AND u.is_active = true;

-- name: MarkPasswordResetTokenUsed :exec
UPDATE password_reset_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL;

-- name: DeletePasswordResetTokensForUser :exec
-- A new forgot-password request invalidates any earlier links and bounds row
-- growth to one live token per user.
DELETE FROM password_reset_tokens WHERE user_id = $1;

-- name: CreateAPIToken :one
-- Personal access token for machine clients. token_hash is
-- SHA-256(raw); the plaintext is returned to the caller once and never stored.
INSERT INTO api_tokens (tenant_id, user_id, name, token_hash, prefix, capability, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetAPITokenAuth :one
-- Hot path: resolve a presented PAT to everything auth.Middleware needs, in one
-- round trip. Joins memberships so `role` is resolved PER REQUEST rather than
-- frozen into the token row — a demotion or org-removal takes effect instantly
-- (the JOIN yields no row once the membership is gone).
--
-- Also filters revoked / expired tokens and deactivated users, so a match here
-- means "this credential is good right now". Callers must treat ErrNoRows as a
-- flat 401 and MUST NOT fall through to JWT validation.
SELECT
    t.id            AS token_id,
    t.tenant_id     AS tenant_id,
    t.user_id       AS user_id,
    t.capability    AS capability,
    t.last_used_at  AS last_used_at,
    m.role          AS role,
    -- Notice-only identity enrichment: user_email in Honeybadger notices for
    -- PAT-authenticated requests (the users JOIN is already here).
    u.email         AS user_email
FROM api_tokens t
JOIN memberships m ON m.user_id = t.user_id AND m.tenant_id = t.tenant_id
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND t.revoked_at IS NULL
  AND t.expires_at > now()
  AND u.is_active = true;

-- name: TouchAPIToken :exec
-- Coarsened last_used_at write: only updates when the stored value is older than
-- the given cutoff, so a busy agent doesn't cause a row write per request.
UPDATE api_tokens
SET last_used_at = now()
WHERE id = $1
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(cutoff)::timestamptz);

-- name: ListAPITokensByUser :many
-- The caller's own tokens in the current org. Never selects token_hash.
SELECT id, tenant_id, user_id, name, prefix, capability, last_used_at, expires_at, revoked_at, created_at
FROM api_tokens
WHERE user_id = $1 AND tenant_id = $2
ORDER BY created_at DESC;

-- name: ListAPITokensByTenant :many
-- Manager view: every token in the org, so a manager can revoke a departing
-- teammate's agent access. Never selects token_hash.
SELECT t.id, t.tenant_id, t.user_id, t.name, t.prefix, t.capability,
       t.last_used_at, t.expires_at, t.revoked_at, t.created_at,
       u.email AS user_email, u.name AS user_name
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.tenant_id = $1
ORDER BY t.created_at DESC;

-- name: GetAPIToken :one
-- Tenant-scoped fetch for the revoke path. Never selects token_hash.
SELECT id, tenant_id, user_id, name, prefix, capability, last_used_at, expires_at, revoked_at, created_at
FROM api_tokens
WHERE id = $1 AND tenant_id = $2;

-- name: RevokeAPIToken :exec
-- Idempotent: re-revoking is a no-op rather than an error. Tenant-scoped so a
-- token ID from another org can never be touched.
UPDATE api_tokens
SET revoked_at = now()
WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL;

-- name: TryLockTenantDelete :one
-- Internal: tenantdelete. The tenant's delete lock, held by the connection
-- for the whole run, so two deletes of one tenant never run together.
SELECT pg_try_advisory_lock(hashtextextended('tenant-delete:' || @tenant_id::bigint, 0))::bool AS locked;

-- name: UnlockTenantDelete :one
-- Internal: tenantdelete. Releases TryLockTenantDelete on the same connection.
SELECT pg_advisory_unlock(hashtextextended('tenant-delete:' || @tenant_id::bigint, 0))::bool AS unlocked;
