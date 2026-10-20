-- Customer entity, tenant-scoped, soft-deletable.
-- All queries filter on tenant_id AND `deleted_at IS NULL` for live customers.

-- name: ListCustomers :many
SELECT * FROM customers
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY lower(name);

-- name: GetCustomer :one
SELECT * FROM customers
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateCustomer :one
INSERT INTO customers (tenant_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: UpdateCustomer :one
UPDATE customers
SET name = $3, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteCustomer :exec
UPDATE customers
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CountProjectsForCustomer :one
-- Used by frontend to warn before delete: "X projects reference this customer."
-- Tenant-scoped via projects.tenant_id (defense in depth).
SELECT count(*) FROM projects
WHERE customer_id = $1 AND tenant_id = $2 AND deleted_at IS NULL;
