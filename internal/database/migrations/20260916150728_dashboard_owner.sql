-- +goose Up
-- Personal dashboards: owner_id NULL = shared with the whole org (every
-- dashboard that exists today), non-NULL = visible only to that user.
--
-- ON DELETE CASCADE rather than SET NULL: a deactivated user's private
-- dashboards must not silently become org-wide.
ALTER TABLE dashboards
    ADD COLUMN owner_id bigint REFERENCES users(id) ON DELETE CASCADE;

-- The old constraint spans both kinds, so two people could not each keep a
-- dashboard called "Weekly". Split it: unique per tenant among SHARED ones,
-- unique per owner among personal ones.
--
-- Both replacements are strictly weaker than what they replace (same columns,
-- plus a predicate), so this cannot fail on existing rows.
ALTER TABLE dashboards DROP CONSTRAINT dashboards_tenant_id_name_key;

CREATE UNIQUE INDEX idx_dashboards_shared_name
    ON dashboards (tenant_id, name) WHERE owner_id IS NULL;
CREATE UNIQUE INDEX idx_dashboards_owner_name
    ON dashboards (tenant_id, owner_id, name) WHERE owner_id IS NOT NULL;

-- Listing is always "the shared ones plus mine".
CREATE INDEX idx_dashboards_tenant_owner ON dashboards (tenant_id, owner_id);

-- +goose Down
DROP INDEX IF EXISTS idx_dashboards_tenant_owner;
DROP INDEX IF EXISTS idx_dashboards_owner_name;
DROP INDEX IF EXISTS idx_dashboards_shared_name;
-- Personal dashboards would violate the restored constraint, so they go first.
DELETE FROM dashboards WHERE owner_id IS NOT NULL;
ALTER TABLE dashboards ADD CONSTRAINT dashboards_tenant_id_name_key UNIQUE (tenant_id, name);
ALTER TABLE dashboards DROP COLUMN owner_id;
