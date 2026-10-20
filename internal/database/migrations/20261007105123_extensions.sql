-- +goose Up
-- Tenant extensions: uploaded zips (manifest + WASM) that provide QE / APE.
-- The zip lives in object storage; the manifest is kept here so listing and
-- validation need no download. One live row per (tenant, ext_id): uploading
-- the same id again replaces the module in place.
CREATE TABLE extensions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ext_id       text NOT NULL,
    name         text NOT NULL,
    version      text NOT NULL,
    api_version  integer NOT NULL,
    capabilities text[] NOT NULL,
    manifest     jsonb NOT NULL,
    storage_key  text NOT NULL,
    sha256       text NOT NULL,
    -- Optional icon PNG (at most 64 KB), shown on the provider tile.
    icon         bytea,
    created_by   bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE UNIQUE INDEX extensions_tenant_ext_live ON extensions (tenant_id, ext_id) WHERE deleted_at IS NULL;

-- The built-in ModelFront integration is replaced by extensions. Its engine
-- rows, keys, connections and vendor rows go; engines left with no rows go
-- with them (router and step references are SET NULL / CASCADE).
CREATE TEMP TABLE mf_engines ON COMMIT DROP AS
SELECT DISTINCT r.engine_id
FROM ai_engine_rows r
LEFT JOIN ai_vendor_tokens t ON t.id = r.token_id
LEFT JOIN ai_vendors v ON v.id = t.vendor_id
WHERE r.provider = 'modelfront' OR v.provider = 'modelfront';

DELETE FROM ai_engine_rows r
USING ai_vendor_tokens t, ai_vendors v
WHERE t.id = r.token_id AND v.id = t.vendor_id AND v.provider = 'modelfront';
DELETE FROM ai_engine_rows WHERE provider = 'modelfront';

DELETE FROM ai_engines e
USING mf_engines m
WHERE e.id = m.engine_id
  AND NOT EXISTS (SELECT 1 FROM ai_engine_rows r WHERE r.engine_id = e.id);

DELETE FROM ai_vendor_tokens t
USING ai_vendors v
WHERE v.id = t.vendor_id AND v.provider = 'modelfront';
DELETE FROM ai_connections c
USING ai_vendors v
WHERE v.id = c.vendor_id AND v.provider = 'modelfront';
DELETE FROM ai_vendors WHERE provider = 'modelfront';

ALTER TABLE tenants DROP COLUMN modelfront_api_key, DROP COLUMN modelfront_base_url;
ALTER TABLE translations DROP COLUMN feedback_sent, DROP COLUMN feedback_sent_at;

-- The analysis band for QE-approved words is provider-neutral.
UPDATE analysis_weights SET band = 'qe' WHERE band = 'modelfront';

-- Timing stage keys follow the code.
UPDATE job_timings
SET stages = (stages - 'ai.predict_modelfront' - 'feedback.modelfront')
    || jsonb_build_object('ai.predict_extension', stages -> 'ai.predict_modelfront')
WHERE stages ? 'ai.predict_modelfront';
UPDATE job_timings
SET stages = stages - 'feedback.modelfront'
WHERE stages ? 'feedback.modelfront';

-- +goose Down
-- Removed ModelFront keys, engines and timings are not restored.
UPDATE job_timings
SET stages = (stages - 'ai.predict_extension')
    || jsonb_build_object('ai.predict_modelfront', stages -> 'ai.predict_extension')
WHERE stages ? 'ai.predict_extension';
UPDATE analysis_weights SET band = 'modelfront' WHERE band = 'qe';
ALTER TABLE translations
    ADD COLUMN feedback_sent boolean DEFAULT false NOT NULL,
    ADD COLUMN feedback_sent_at timestamp with time zone;
ALTER TABLE tenants
    ADD COLUMN modelfront_api_key text,
    ADD COLUMN modelfront_base_url text DEFAULT 'https://api.modelfront.com/v1';
DELETE FROM ai_vendors WHERE provider LIKE 'ext:%';
DROP TABLE extensions;
