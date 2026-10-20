-- +goose Up
-- Step 1 of ditching ai_providers.
--
-- An AI system row pointed at an `ai_providers` row for its {model, token,
-- settings}. That indirection buys nothing — a system is exactly one row,
-- so a "reusable named model config" is a level of naming nobody
-- reuses. Move the three fields onto the row itself.
--
-- After this migration nothing READS ai_system_rows.provider_id; it stays as a
-- dead column so step 2 is a pure DROP of the column and the ai_providers table.

ALTER TABLE ai_system_rows
    ADD COLUMN token_id bigint REFERENCES ai_vendor_tokens(id) ON DELETE RESTRICT,
    ADD COLUMN model    text  NOT NULL DEFAULT '',
    ADD COLUMN settings jsonb NOT NULL DEFAULT '{}'::jsonb;

-- Backfill every existing row from the provider it referenced. A row whose
-- provider is missing (or whose provider has no token) keeps token_id NULL and
-- is treated exactly like today's "row has no provider" case: the pipeline
-- benign-skips it and the UI asks for a token on next edit.
UPDATE ai_system_rows r
SET token_id = p.token_id,
    model    = p.model,
    settings = p.settings
FROM ai_providers p
WHERE p.id = r.provider_id;

CREATE INDEX idx_ai_system_rows_token ON ai_system_rows (token_id);

-- +goose Down
DROP INDEX IF EXISTS idx_ai_system_rows_token;
ALTER TABLE ai_system_rows
    DROP COLUMN token_id,
    DROP COLUMN model,
    DROP COLUMN settings;
