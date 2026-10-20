-- +goose NO TRANSACTION
-- +goose Up
-- One tenant's history in a time range: every dashboard measure on
-- translation_history filters on exactly this. CONCURRENTLY keeps writes
-- flowing while it builds; a failed build leaves an invalid index behind, so
-- it is dropped first and a re-run builds it again.
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tenant_time;
CREATE INDEX CONCURRENTLY idx_translation_history_tenant_time
    ON translation_history (tenant_id, created_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_translation_history_tenant_time;
