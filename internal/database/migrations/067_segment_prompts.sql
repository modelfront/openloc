-- +goose Up
-- Segment prompt templates: a reusable name + instruction text with
-- placeholders, applied per segment. Tenant-scoped. Config only — nothing
-- executes these against a provider yet (mirrors ai_prompts staging).
CREATE TABLE segment_prompts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    prompt_text text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX idx_segment_prompts_tenant ON segment_prompts (tenant_id);

-- +goose Down
DROP TABLE segment_prompts;
