-- +goose Up
-- AI prompt templates. Tenant-scoped prompt definitions used to build
-- AI engines (QE+APE / MT). A prompt carries its instruction text, the set of
-- output fields the model is expected to emit (status / text / note / comments),
-- and optional scoping metadata (provider, language pair, step, customer, domain)
-- that AI engines inherit. Config only — nothing executes these yet.
CREATE TABLE ai_prompts (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            text NOT NULL,
    prompt_text     text NOT NULL,
    -- Output-format flags. At least one is enforced true in the handler.
    out_status      boolean NOT NULL DEFAULT false,
    out_text        boolean NOT NULL DEFAULT false,
    out_note        boolean NOT NULL DEFAULT false,
    out_comments    boolean NOT NULL DEFAULT false,
    -- Optional scoping metadata (inherited by AI engines built on this prompt).
    provider        text,
    source_language text,
    target_language text,
    step            text,
    customer_id     bigint REFERENCES customers(id) ON DELETE SET NULL,
    domain          text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX idx_ai_prompts_tenant ON ai_prompts (tenant_id);

-- +goose Down
DROP TABLE ai_prompts;
