-- +goose Up
ALTER TYPE file_format ADD VALUE 'xlsx';

-- +goose Down
-- PostgreSQL does not support removing enum values.
-- To roll back, recreate the enum without 'xlsx' (requires dropping dependent columns).
