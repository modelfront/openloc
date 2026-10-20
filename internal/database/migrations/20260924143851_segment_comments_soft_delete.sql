-- +goose Up
-- Deleting a comment from the editor or the assistant is a soft delete.
-- Reads filter deleted_at IS NULL.
ALTER TABLE segment_comments ADD COLUMN deleted_at timestamptz;

-- +goose Down
DELETE FROM segment_comments WHERE deleted_at IS NOT NULL;
ALTER TABLE segment_comments DROP COLUMN deleted_at;
