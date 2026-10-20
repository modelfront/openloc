-- Mark the built-in "Default" segment prompt so it can be rendered read-only and
-- undeletable (mirrors ai_prompts.is_default). Existing default rows (named
-- "Default", or the pre-rename "Default segment prompt") are backfilled.
-- +goose Up
ALTER TABLE segment_prompts ADD COLUMN is_default boolean NOT NULL DEFAULT false;
UPDATE segment_prompts SET is_default = true WHERE name IN ('Default', 'Default segment prompt');

-- +goose Down
ALTER TABLE segment_prompts DROP COLUMN is_default;
