-- +goose Up
-- Step 2 of removing ai_providers.
--
-- Step 1 moved {token_id, model, settings} onto ai_system_rows and
-- backfilled every row, leaving provider_id dead. Nothing has read either since.
-- Vendors and tokens stay — those are the real credential store.

ALTER TABLE ai_system_rows DROP COLUMN provider_id;
DROP TABLE ai_providers;

-- +goose Down
-- Recreates the shape (migrations 072 + 080 + 083), NOT the data: the configs
-- were named model presets derived from what the rows already carry, so a
-- down-migration that invented names would be worse than an empty table. Rows
-- keep their own token/model/settings and stay fully functional with
-- provider_id NULL.
--
-- NOTE: no UNIQUE (tenant_id, name) — migration 083 dropped it, and its own
-- Down re-adds it. Recreating it here makes the full down-chain collide with
-- "relation ai_providers_tenant_id_name_key already exists".
CREATE TABLE ai_providers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    model      text NOT NULL,
    settings   jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    token_id   bigint REFERENCES ai_vendor_tokens(id) ON DELETE RESTRICT
);
CREATE INDEX idx_ai_providers_tenant ON ai_providers (tenant_id);
CREATE INDEX idx_ai_providers_token ON ai_providers (token_id);

ALTER TABLE ai_system_rows ADD COLUMN provider_id bigint REFERENCES ai_providers(id) ON DELETE SET NULL;
