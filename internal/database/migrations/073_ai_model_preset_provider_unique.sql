-- +goose Up
-- Model-preset names are unique per *provider*, not tenant-wide.
-- "Default" is the natural preset name for every provider, so a
-- tenant-wide UNIQUE(tenant_id, name) blocked a second provider's "Default" and
-- surfaced as an opaque 500 on create/rename. Scope uniqueness to the provider.
ALTER TABLE ai_model_presets DROP CONSTRAINT ai_model_presets_tenant_id_name_key;
ALTER TABLE ai_model_presets ADD CONSTRAINT ai_model_presets_tenant_provider_name_key
    UNIQUE (tenant_id, provider, name);

-- +goose Down
ALTER TABLE ai_model_presets DROP CONSTRAINT ai_model_presets_tenant_provider_name_key;
ALTER TABLE ai_model_presets ADD CONSTRAINT ai_model_presets_tenant_id_name_key
    UNIQUE (tenant_id, name);
