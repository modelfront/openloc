-- The shared demo organization (tenants.kind = 'demo').
-- There is at most one (unique index tenants_one_demo), so these queries find
-- it by kind rather than by id.

-- name: LockDemoOrganization :exec
-- Serialises demoorg.Ensure across replicas for the rest of the transaction.
SELECT pg_advisory_xact_lock(hashtextextended('demo_organization', 0));

-- name: GetDemoTenant :one
-- Internal: the one demo tenant, not scoped by a caller's tenant.
SELECT * FROM tenants WHERE kind = 'demo';

-- name: CreateDemoTenant :one
-- Internal: the demo tenant has no domain and no open registration, so nobody
-- joins it by signing up; it is entered, never joined.
INSERT INTO tenants (name, slug, domain, open_registration, status, kind, demo_version, demo_built_at)
VALUES (@name, @slug, NULL, false, 'approved', 'demo', @demo_version, @demo_built_at)
RETURNING *;
