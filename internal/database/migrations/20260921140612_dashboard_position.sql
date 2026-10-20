-- +goose Up
-- Manual dashboard order. Dashboards sorted by
-- NAME, which makes the list alphabetical rather than useful — the one people
-- open daily sits wherever its initial puts it.
--
-- Mirrors dashboard_charts.position: one integer per row, written as a whole
-- order in a transaction. Default 0 for every existing row, and the read keeps
-- `name` as the tiebreaker, so nothing moves until someone drags something.
ALTER TABLE dashboards ADD COLUMN position int NOT NULL DEFAULT 0;

-- Matches the read: shared first, then position, then name.
CREATE INDEX idx_dashboards_tenant_position ON dashboards (tenant_id, owner_id, position);

-- +goose Down
DROP INDEX IF EXISTS idx_dashboards_tenant_position;
ALTER TABLE dashboards DROP COLUMN position;
