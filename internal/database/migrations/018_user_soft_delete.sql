-- +goose Up
-- +goose StatementBegin

-- Soft-delete for users (per j20 / industry convention).
--
-- Users are soft-deleted rather than hard-deleted to preserve identity so `edited_by` continues to resolve to a real
-- username, and they preserve group memberships for one-click reactivation.
-- Hard-delete with ON DELETE SET NULL would silently rewrite history to
-- "Deleted user" — that's worse.
--
-- Auth middleware extends its UserExists check to reject inactive users —
-- a deactivated user's JWT 401s on the next request.

ALTER TABLE users
    ADD COLUMN is_active boolean NOT NULL DEFAULT true,
    ADD COLUMN deactivated_at timestamptz;

-- Partial index — most lookups go through tenant_id+active. The full users
-- table stays small so this is cheap; it pays off in ListUsersByTenant.
CREATE INDEX idx_users_tenant_active ON users (tenant_id) WHERE is_active = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_users_tenant_active;

ALTER TABLE users
    DROP COLUMN IF EXISTS deactivated_at,
    DROP COLUMN IF EXISTS is_active;

-- +goose StatementEnd
