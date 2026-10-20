-- +goose Up
-- Soft delete for MT engines, AI engines and QA profiles.
-- Live reads filter deleted_at IS NULL. None of the three has a unique name,
-- so a deleted row never blocks reusing its name.
ALTER TABLE mt_engines ADD COLUMN deleted_at timestamptz;
ALTER TABLE ai_engines ADD COLUMN deleted_at timestamptz;
ALTER TABLE qa_profiles ADD COLUMN deleted_at timestamptz;

-- +goose Down
-- Deleted rows go away for real on the way down, so the old code (which
-- reads every row) does not resurrect them.
DELETE FROM mt_engines WHERE deleted_at IS NOT NULL;
DELETE FROM ai_engines WHERE deleted_at IS NOT NULL;
DELETE FROM qa_profiles WHERE deleted_at IS NOT NULL;
ALTER TABLE qa_profiles DROP COLUMN deleted_at;
ALTER TABLE ai_engines DROP COLUMN deleted_at;
ALTER TABLE mt_engines DROP COLUMN deleted_at;
