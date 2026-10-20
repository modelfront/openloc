-- +goose Up
-- A deleted TM keeps its row and entries; reads filter
-- deleted_at IS NULL.
ALTER TABLE tm_collections ADD COLUMN deleted_at timestamptz;

-- +goose Down
DELETE FROM tm_collections WHERE deleted_at IS NOT NULL;
ALTER TABLE tm_collections DROP COLUMN deleted_at;
