-- +goose Up
-- An MT engine can be OWNED by an AI settings token.
--
-- Adding a DeepL/Google key on the AI settings page now mirrors it into an
-- mt_engines row, because that row is still what the translate pipeline reads
-- credentials from. Without this the key was stored and verified but inert.
--
-- managed_token_id NULL = a hand-made engine, behaving exactly as before.
-- Non-NULL = this row is a projection of an AI token: the AI settings page owns
-- its name, type, key and endpoint, and the MT engines page must not delete it
-- or edit those fields, or the two copies of one credential would diverge.
--
-- ON DELETE CASCADE: removing the key removes the engine it projected. That
-- also drops its settings_mt_assignments rows, which is why the token delete
-- handler refuses while any assignment still points at it — the cascade is the
-- mechanism, not the policy.
ALTER TABLE mt_engines
    ADD COLUMN managed_token_id bigint REFERENCES ai_vendor_tokens(id) ON DELETE CASCADE;

-- One engine per token: the upsert on token save keys off this, and two rows
-- for one credential is the exact drift the column exists to prevent.
CREATE UNIQUE INDEX idx_mt_engines_managed_token ON mt_engines (managed_token_id)
    WHERE managed_token_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_mt_engines_managed_token;
ALTER TABLE mt_engines DROP COLUMN managed_token_id;
