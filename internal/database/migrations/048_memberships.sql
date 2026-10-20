-- +goose Up
-- Multi-org support: split identity (users) from org membership.
-- A user is now a global identity (one row per person, email globally unique)
-- that can belong to many tenants via a membership. Role moves off the user
-- row onto the membership — a person can be owner in one org and linguist in
-- another. This migration ADDS the join table and backfills; migration 049
-- then removes the now-redundant users.tenant_id / users.role.
CREATE TABLE memberships (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role       text   NOT NULL DEFAULT 'linguist',   -- four-role model (045); no enum/CHECK per convention
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, tenant_id)
);

CREATE INDEX idx_memberships_user   ON memberships USING btree (user_id);
CREATE INDEX idx_memberships_tenant ON memberships USING btree (tenant_id);

-- Backfill: one membership per existing user, carrying its current tenant+role.
-- Runs BEFORE migration 049 drops these columns — ordering is load-bearing.
INSERT INTO memberships (user_id, tenant_id, role, created_at)
SELECT id, tenant_id, role, created_at FROM users;

-- +goose Down
DROP TABLE memberships;
