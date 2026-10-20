-- +goose Up
-- Redesign the AI provider model into three levels (product request):
--   ai_vendors        — OpenAI / Claude / ModelFront (one row per tenant+vendor)
--   ai_vendor_tokens  — many named API keys per vendor (key moves here)
--   ai_providers      — the old "model preset": a named {model, token, settings}
-- The name `ai_providers` is REUSED: the old connection table becomes ai_vendors,
-- and ai_model_presets becomes the new ai_providers. Order matters below.

-- 1. Old connection registry becomes the vendor.
ALTER TABLE ai_providers RENAME TO ai_vendors;

-- 2. Tokens table — the encrypted key moves off the vendor onto named tokens.
CREATE TABLE ai_vendor_tokens (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vendor_id         bigint NOT NULL REFERENCES ai_vendors(id) ON DELETE CASCADE,
    name              text NOT NULL,
    api_key_encrypted text NOT NULL,
    base_url          text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, name)
);
CREATE INDEX idx_ai_vendor_tokens_vendor ON ai_vendor_tokens (vendor_id);

-- 3. Backfill: each existing vendor's single key becomes its "Default" token.
INSERT INTO ai_vendor_tokens (vendor_id, name, api_key_encrypted, base_url)
SELECT id, 'Default', api_key_encrypted, base_url FROM ai_vendors;

ALTER TABLE ai_vendors
    DROP COLUMN api_key_encrypted,
    DROP COLUMN base_url,
    DROP COLUMN connected_at;

-- 4. Old model preset becomes the new "provider" (a named model config).
ALTER TABLE ai_model_presets RENAME TO ai_providers;
ALTER INDEX idx_ai_model_presets_tenant RENAME TO idx_ai_providers_tenant;

-- 5. Point each provider at a vendor token, backfilled from its old provider text.
ALTER TABLE ai_providers ADD COLUMN token_id bigint REFERENCES ai_vendor_tokens(id) ON DELETE RESTRICT;
UPDATE ai_providers p
SET token_id = t.id
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
WHERE v.tenant_id = p.tenant_id AND v.provider = p.provider AND t.name = 'Default';

-- Uniqueness moves from (tenant, provider, name) to (tenant, name); provider is
-- now derived via token → vendor, so drop the text column.
ALTER TABLE ai_providers DROP CONSTRAINT ai_model_presets_tenant_provider_name_key;
ALTER TABLE ai_providers ADD CONSTRAINT ai_providers_tenant_id_name_key UNIQUE (tenant_id, name);
ALTER TABLE ai_providers DROP COLUMN provider;
-- token_id is intentionally NULLABLE: a config may predate its vendor's connection
-- (backfill can't invent a key). New configs require a token in the handler.
CREATE INDEX idx_ai_providers_token ON ai_providers (token_id);

-- 6. AI system rows referenced the preset; rename the FK column.
ALTER TABLE ai_system_rows RENAME COLUMN model_preset_id TO provider_id;

-- +goose Down
ALTER TABLE ai_system_rows RENAME COLUMN provider_id TO model_preset_id;

DROP INDEX idx_ai_providers_token;
ALTER TABLE ai_providers ADD COLUMN provider text;
UPDATE ai_providers p
SET provider = v.provider
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
WHERE p.token_id = t.id;
UPDATE ai_providers SET provider = 'openai' WHERE provider IS NULL;
ALTER TABLE ai_providers ALTER COLUMN provider SET NOT NULL;
ALTER TABLE ai_providers DROP CONSTRAINT ai_providers_tenant_id_name_key;
ALTER TABLE ai_providers ADD CONSTRAINT ai_model_presets_tenant_provider_name_key UNIQUE (tenant_id, provider, name);
ALTER TABLE ai_providers DROP COLUMN token_id;
ALTER INDEX idx_ai_providers_tenant RENAME TO idx_ai_model_presets_tenant;
ALTER TABLE ai_providers RENAME TO ai_model_presets;

ALTER TABLE ai_vendors
    ADD COLUMN api_key_encrypted text,
    ADD COLUMN base_url text,
    ADD COLUMN connected_at timestamptz NOT NULL DEFAULT now();
UPDATE ai_vendors v
SET api_key_encrypted = t.api_key_encrypted, base_url = t.base_url
FROM ai_vendor_tokens t
WHERE t.vendor_id = v.id AND t.name = 'Default';
UPDATE ai_vendors SET api_key_encrypted = '' WHERE api_key_encrypted IS NULL;
ALTER TABLE ai_vendors ALTER COLUMN api_key_encrypted SET NOT NULL;
DROP TABLE ai_vendor_tokens;
ALTER TABLE ai_vendors RENAME TO ai_providers;
