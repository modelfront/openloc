-- +goose Up
-- Multi-org support, part 2. Now that memberships (048) hold the
-- authoritative (user, tenant, role) mapping, remove the single-tenant vestiges
-- from users:
--   * tenant_id → last_tenant_id: nullable, NO LONGER an isolation key. It is
--     only a convenience pointer used to default the org picker. Deleting a
--     tenant must NOT delete the identity, so the FK becomes ON DELETE SET NULL.
--   * role: dropped entirely — role now lives on the membership.
-- password_hash stays as a vestigial column (login is Google/dev-only now).

-- Drop tenant indexes first: a bare RENAME would silently retarget the partial
-- index expression, and neither index is needed once tenant_id is not a key.
DROP INDEX IF EXISTS idx_users_tenant;
DROP INDEX IF EXISTS idx_users_tenant_active;

ALTER TABLE users DROP CONSTRAINT users_tenant_id_fkey;
ALTER TABLE users RENAME COLUMN tenant_id TO last_tenant_id;
ALTER TABLE users ALTER COLUMN last_tenant_id DROP NOT NULL;
ALTER TABLE users
    ADD CONSTRAINT users_last_tenant_id_fkey
    FOREIGN KEY (last_tenant_id) REFERENCES tenants(id) ON DELETE SET NULL;

ALTER TABLE users DROP COLUMN role;

-- +goose Down
ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'linguist';
UPDATE users u SET role = COALESCE(
    (SELECT m.role FROM memberships m WHERE m.user_id = u.id ORDER BY m.created_at LIMIT 1),
    'linguist');

ALTER TABLE users DROP CONSTRAINT users_last_tenant_id_fkey;
UPDATE users u SET last_tenant_id = COALESCE(
    last_tenant_id,
    (SELECT m.tenant_id FROM memberships m WHERE m.user_id = u.id ORDER BY m.created_at LIMIT 1));
-- NOTE: a 0-membership identity has no tenant to restore — this SET NOT NULL
-- will fail for such rows. Acceptable: the down path is only for local rollback
-- before any 0-org identities exist.
ALTER TABLE users ALTER COLUMN last_tenant_id SET NOT NULL;
ALTER TABLE users RENAME COLUMN last_tenant_id TO tenant_id;
ALTER TABLE users
    ADD CONSTRAINT users_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX idx_users_tenant ON users USING btree (tenant_id);
CREATE INDEX idx_users_tenant_active ON users (tenant_id) WHERE is_active = true;
