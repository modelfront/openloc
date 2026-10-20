-- +goose Up
-- Saved analyses are user entities: deleting one sets deleted_at and every
-- read filters it. Dashboard charts get their soft delete
-- from 20260925062917_dashboard_soft_delete.
--
-- The pending-auto-analysis coalescing index skips deleted rows, so a deleted
-- pending auto analysis never absorbs a later upload.

ALTER TABLE analyses ADD COLUMN deleted_at timestamptz;

DROP INDEX uq_pending_auto_analysis;
CREATE UNIQUE INDEX uq_pending_auto_analysis
    ON analyses (project_id)
    WHERE is_auto AND status = 'pending' AND deleted_at IS NULL;

-- +goose Down
-- Soft-deleted rows would come back as live ones without the column, so they
-- are removed for real.
DELETE FROM analyses WHERE deleted_at IS NOT NULL;

DROP INDEX uq_pending_auto_analysis;
CREATE UNIQUE INDEX uq_pending_auto_analysis
    ON analyses (project_id)
    WHERE is_auto AND status = 'pending';

ALTER TABLE analyses DROP COLUMN deleted_at;
