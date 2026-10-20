-- +goose Up
-- PowerPoint (.pptx) source files.
-- IF NOT EXISTS keeps this re-runnable: the Down below can't remove the
-- label, so a down+re-up cycle would otherwise fail with "enum label already
-- exists".
ALTER TYPE file_format ADD VALUE IF NOT EXISTS 'pptx';

-- +goose Down
-- PostgreSQL does not support removing enum values.
-- To roll back, recreate the enum without 'pptx' (requires dropping dependent columns).
