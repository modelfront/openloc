-- +goose Up
-- An MT engine is CONFIGURED, not projected.
--
-- It names a key from AI settings and carries the request options that key's
-- provider supports, so one DeepL account can back several engines that differ
-- only in how they translate — formal and informal, glossary and no glossary.
--
-- token_id replaces managed_token_id. The old column meant "this row IS a
-- projection of that key, and AI settings owns every field"; the new one means
-- "this row SPENDS that key", which is a reference, not ownership. Hence
-- RESTRICT rather than CASCADE: removing a key must not silently take engines
-- (and the router rules hanging off them) with it. The token handler refuses
-- first with a message; this is the backstop.
--
-- NULL token_id = a deployment-owned engine (LibreTranslate, mock), whose
-- credential comes from the environment and never from a user's key.
ALTER TABLE mt_engines
    ADD COLUMN token_id bigint REFERENCES ai_vendor_tokens(id) ON DELETE RESTRICT,
    ADD COLUMN settings jsonb NOT NULL DEFAULT '{}'::jsonb;

-- Every projected row becomes a configured row spending the same key, so a
-- tenant keeps the engines its routers already point at.
UPDATE mt_engines SET token_id = managed_token_id WHERE managed_token_id IS NOT NULL;

DROP INDEX IF EXISTS idx_mt_engines_managed_token;
ALTER TABLE mt_engines DROP COLUMN managed_token_id;

-- Several engines per key is the point, so this is NOT unique. It exists for
-- the delete guard, which counts a key's engines on every token delete.
CREATE INDEX idx_mt_engines_token ON mt_engines (token_id) WHERE token_id IS NOT NULL;

-- +goose Down
ALTER TABLE mt_engines
    ADD COLUMN managed_token_id bigint REFERENCES ai_vendor_tokens(id) ON DELETE CASCADE;

-- Only the FIRST engine per key can go back: the old column is unique per
-- token, so a key with several engines cannot be represented. The rest revert
-- to hand-made engines, which is what they look like without the column.
UPDATE mt_engines m SET managed_token_id = m.token_id
WHERE m.token_id IS NOT NULL
  AND m.id = (SELECT min(id) FROM mt_engines x WHERE x.token_id = m.token_id);

CREATE UNIQUE INDEX idx_mt_engines_managed_token ON mt_engines (managed_token_id)
    WHERE managed_token_id IS NOT NULL;

DROP INDEX IF EXISTS idx_mt_engines_token;
ALTER TABLE mt_engines DROP COLUMN token_id, DROP COLUMN settings;
