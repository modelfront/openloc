-- +goose Up
-- AI engines. Tenant-scoped. An AI engine binds a prompt to a type
-- (qe = quality estimation, ape = automatic post-editing, mt = machine
-- translation) plus a policy for how to treat bad model output. The engine's
-- provider / language pair / step / customer / domain are INHERITED from the
-- referenced prompt (read via JOIN) — `provider` here is only a fallback used
-- when the prompt itself declares no provider. Config only — not executed yet.
--
-- engine_type ∈ {qe, ape, mt}; bad_output_action ∈ {reject, comment, ignore}.
-- The type↔output-format rule (mt/ape require prompt.out_text; qe requires
-- prompt.out_status) is enforced in the handler, not the schema.
CREATE TABLE ai_engines (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name              text NOT NULL,
    engine_type       text NOT NULL,
    prompt_id         bigint NOT NULL REFERENCES ai_prompts(id) ON DELETE RESTRICT,
    provider          text,
    bad_output_action text NOT NULL DEFAULT 'reject',
    enabled           boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX idx_ai_engines_tenant ON ai_engines (tenant_id);
CREATE INDEX idx_ai_engines_prompt ON ai_engines (prompt_id);

-- +goose Down
DROP TABLE ai_engines;
