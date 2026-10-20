-- +goose Up
-- AI provider connections (connection registry). Tenant-scoped;
-- one row per (tenant, provider). The API key is stored encrypted at rest
-- (AES-256-GCM via internal/secrets) — never in plaintext, never returned to
-- the client. `provider` is one of 'modelfront' | 'openai' | 'claude'.
CREATE TABLE ai_providers (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider          text NOT NULL,
    api_key_encrypted text NOT NULL,
    base_url          text,
    connected_at      timestamptz NOT NULL DEFAULT now(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, provider)
);

-- +goose Down
DROP TABLE ai_providers;
