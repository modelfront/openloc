-- +goose Up
ALTER TABLE translations ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE translations DROP COLUMN IF EXISTS locked;
