-- +goose Up
-- AI-as-MT: an MT-type AI engine has no "on bad output" policy — its
-- text is used directly as the machine translation. Make bad_output_action
-- nullable and drop the column default so `mt` engines persist NULL; the handler
-- still requires + validates a value for `qe`/`ape` engines.
ALTER TABLE ai_engines ALTER COLUMN bad_output_action DROP NOT NULL;
ALTER TABLE ai_engines ALTER COLUMN bad_output_action DROP DEFAULT;

-- +goose Down
UPDATE ai_engines SET bad_output_action = 'reject' WHERE bad_output_action IS NULL;
ALTER TABLE ai_engines ALTER COLUMN bad_output_action SET DEFAULT 'reject';
ALTER TABLE ai_engines ALTER COLUMN bad_output_action SET NOT NULL;
