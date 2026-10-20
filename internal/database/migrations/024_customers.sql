-- +goose Up
-- +goose StatementBegin

-- Customer entity for project organization. Tenant-scoped, soft-deletable.
--
-- Why soft delete (`deleted_at`):
--   Projects may keep historical references to a "deleted" customer. We want
--   the customer name to survive in audit even after removal. Frontend lists
--   filter on `deleted_at IS NULL`; the project detail / report view can show
--   the original customer name even if the customer was tombstoned.
--
-- Why unique on `(tenant_id, lower(name)) WHERE deleted_at IS NULL`:
--   Prevents "Acme" + "ACME" from coexisting in the same tenant — case-
--   insensitive comparison without storing a normalized column. The partial
--   predicate means a re-created "Acme" doesn't collide with a tombstoned one.
--
-- Why no contact info fields (email / phone / address):
--   Defer to first customer request. Schema doesn't preclude adding them.
--
-- Cross-tenant scope: each tenant maintains its own customer list. Two
-- different tenants can both have a customer named "Acme Corp" with different
-- IDs. This is intentional — multi-tenant separation is absolute.

CREATE TABLE customers (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE UNIQUE INDEX idx_customers_tenant_name
    ON customers (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE INDEX idx_customers_tenant
    ON customers (tenant_id) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_customers_tenant;
DROP INDEX IF EXISTS idx_customers_tenant_name;
DROP TABLE IF EXISTS customers;

-- +goose StatementEnd
