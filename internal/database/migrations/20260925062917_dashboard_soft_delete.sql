-- +goose Up
-- Dashboards and charts are user entities, so deleting one from the UI or the
-- chat soft-deletes it: the row stays, marked, and every
-- read filters it out.
ALTER TABLE dashboards       ADD COLUMN deleted_at timestamptz;
ALTER TABLE dashboard_charts ADD COLUMN deleted_at timestamptz;

-- A deleted dashboard's name is free again. The unique indexes cover live rows
-- only, or deleting "Weekly" would forbid ever making another one.
DROP INDEX IF EXISTS idx_dashboards_shared_name;
DROP INDEX IF EXISTS idx_dashboards_owner_name;
CREATE UNIQUE INDEX idx_dashboards_shared_name
    ON dashboards (tenant_id, name) WHERE owner_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX idx_dashboards_owner_name
    ON dashboards (tenant_id, owner_id, name) WHERE owner_id IS NOT NULL AND deleted_at IS NULL;

-- +goose Down
-- Deleted rows go for good on the way down: without the column there is no
-- way to keep them hidden, and restoring them would resurrect deleted work.
DELETE FROM dashboard_charts WHERE deleted_at IS NOT NULL;
DELETE FROM dashboards WHERE deleted_at IS NOT NULL;
DROP INDEX IF EXISTS idx_dashboards_owner_name;
DROP INDEX IF EXISTS idx_dashboards_shared_name;
CREATE UNIQUE INDEX idx_dashboards_shared_name
    ON dashboards (tenant_id, name) WHERE owner_id IS NULL;
CREATE UNIQUE INDEX idx_dashboards_owner_name
    ON dashboards (tenant_id, owner_id, name) WHERE owner_id IS NOT NULL;
ALTER TABLE dashboard_charts DROP COLUMN deleted_at;
ALTER TABLE dashboards       DROP COLUMN deleted_at;
