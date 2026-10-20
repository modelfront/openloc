-- +goose Up
-- Model presets: a named (provider, model, settings) combo,
-- configured under "Providers and models" and attached to an AI engine row.
-- Provider/model/settings move OFF ai_prompts (a prompt is now just system-prompt
-- text + output mode) and ONTO presets, which engine rows reference.
CREATE TABLE ai_model_presets (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    provider   text NOT NULL,
    model      text NOT NULL,
    settings   jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX idx_ai_model_presets_tenant ON ai_model_presets (tenant_id);

-- An engine row's LLM model comes from a preset (NULL for ModelFront-native rows).
ALTER TABLE ai_engine_rows
    ADD COLUMN model_preset_id bigint REFERENCES ai_model_presets(id) ON DELETE RESTRICT;

-- Provider/model/settings now live on presets, not prompts.
ALTER TABLE ai_prompts
    DROP COLUMN provider,
    DROP COLUMN model,
    DROP COLUMN model_config;

-- +goose Down
ALTER TABLE ai_prompts
    ADD COLUMN provider text,
    ADD COLUMN model text,
    ADD COLUMN model_config jsonb;
ALTER TABLE ai_engine_rows DROP COLUMN model_preset_id;
DROP TABLE ai_model_presets;
