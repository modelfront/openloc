-- +goose Up
-- The per-segment user message stops being a tenant-authored template and
-- becomes a generated JSON object: the system prompt names which context each
-- segment carries, and the body is built from that.
--
-- Only the OPTIONAL context lives here. The source text (and, for a reviewing
-- mode, the translation) are implied by the prompt's output mode — there is
-- nothing to translate or judge without them — so they are not stored as
-- choices. See segmentprompt.FieldsFor.
ALTER TABLE ai_prompts
    ADD COLUMN segment_context text[] NOT NULL DEFAULT '{}';

-- Carry over what each prompt's segment templates actually used. A prompt can
-- be paired with different segment prompts on different engine rows, so the
-- keys are unioned across them (bool_or) — dropping context a row relied on
-- would silently change what that engine sends.
--
-- The CASE chain builds the array in canonical order, and each key can only be
-- added once, so no de-duplication is needed.
UPDATE ai_prompts p
SET segment_context = sub.ctx
FROM (
    SELECT r.system_prompt_id AS prompt_id,
           ARRAY[]::text[]
           || CASE WHEN bool_or(sp.prompt_text LIKE '%<tm_match>%'
                             OR sp.prompt_text LIKE '%<tm_match_source>%')
                   THEN ARRAY['tm_match'] ELSE ARRAY[]::text[] END
           || CASE WHEN bool_or(sp.prompt_text LIKE '%<note>%')
                   THEN ARRAY['note'] ELSE ARRAY[]::text[] END
           || CASE WHEN bool_or(sp.prompt_text LIKE '%<comments>%')
                   THEN ARRAY['comments'] ELSE ARRAY[]::text[] END
           AS ctx
    FROM ai_engine_rows r
    JOIN segment_prompts sp ON sp.id = r.segment_prompt_id
    WHERE r.system_prompt_id IS NOT NULL
    GROUP BY r.system_prompt_id
) sub
WHERE p.id = sub.prompt_id;

ALTER TABLE ai_engine_rows DROP COLUMN segment_prompt_id;
DROP TABLE segment_prompts;

-- +goose Down
-- The templates themselves are not recoverable: they were converted to a set of
-- context keys, which cannot be turned back into prose. This restores the
-- SHAPE, not the content — including every index and constraint the earlier
-- migrations added, because their own Down steps drop them by name and would
-- fail against a partially rebuilt table.
CREATE TABLE segment_prompts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    prompt_text text NOT NULL,
    is_default  boolean NOT NULL DEFAULT false,
    default_for text CHECK (default_for IS NULL OR default_for IN ('mt', 'qe_ape')),
    purpose     text NOT NULL DEFAULT 'qe_ape' CHECK (purpose IN ('mt', 'qe_ape')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name, purpose)
);
CREATE INDEX idx_segment_prompts_tenant ON segment_prompts (tenant_id);
CREATE INDEX idx_segment_prompts_tenant_purpose ON segment_prompts (tenant_id, purpose);
CREATE UNIQUE INDEX uq_segment_prompt_default_for
    ON segment_prompts (tenant_id, default_for)
    WHERE default_for IS NOT NULL;

ALTER TABLE ai_engine_rows
    ADD COLUMN segment_prompt_id bigint REFERENCES segment_prompts(id) ON DELETE SET NULL;

ALTER TABLE ai_prompts DROP COLUMN segment_context;
