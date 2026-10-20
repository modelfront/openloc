-- +goose Up
-- Removing an "Ignore all" word sets deleted_at; reads skip those rows, and
-- adding the word again clears it.
ALTER TABLE spellcheck_dictionary ADD COLUMN deleted_at timestamptz;

-- +goose Down
DELETE FROM spellcheck_dictionary WHERE deleted_at IS NOT NULL;
ALTER TABLE spellcheck_dictionary DROP COLUMN deleted_at;
