-- +goose Up
ALTER TABLE translations ADD COLUMN active_rich_text TEXT;

-- +goose Down
ALTER TABLE translations DROP COLUMN IF EXISTS active_rich_text;
