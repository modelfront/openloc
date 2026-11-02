-- +goose Up
-- The issue lists' default order, and every order's tie-breaker, is newest
-- report first: one index for an org's list and one for the operators' list
-- across orgs.
CREATE INDEX issues_tenant_created_idx ON issues (tenant_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX issues_created_idx ON issues (created_at DESC, id DESC) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS issues_created_idx;
DROP INDEX IF EXISTS issues_tenant_created_idx;
