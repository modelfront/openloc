-- +goose Up
-- The vendor an AI engine row's key belongs to. It survives the key's
-- deletion (token_id goes NULL), so a keyless engine still knows which
-- vendor's key can fix it. Rows already keyless keep NULL: nothing records
-- their vendor any more.
ALTER TABLE ai_engine_rows ADD COLUMN provider text;

UPDATE ai_engine_rows r
SET provider = v.provider
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
WHERE t.id = r.token_id;

-- +goose Down
ALTER TABLE ai_engine_rows DROP COLUMN provider;
