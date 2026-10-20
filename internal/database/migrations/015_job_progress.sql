-- +goose Up
ALTER TABLE jobs ADD COLUMN translated_count integer;
ALTER TABLE jobs ADD COLUMN total_count integer;

-- +goose Down
ALTER TABLE jobs DROP COLUMN IF EXISTS total_count;
ALTER TABLE jobs DROP COLUMN IF EXISTS translated_count;
