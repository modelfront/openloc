-- +goose Up
-- Reshape AI engines: an engine is now just a name plus a
-- set of language-routing rows. Each row maps a source language + target
-- languages to a system prompt, an optional segment prompt, and a bad-output
-- action. Provider/model are inherited from the chosen system prompt. Config
-- only — runtime resolution against this model isn't wired yet.
ALTER TABLE ai_engines
    DROP COLUMN engine_type,
    DROP COLUMN prompt_id,
    DROP COLUMN provider,
    DROP COLUMN bad_output_action,
    DROP COLUMN enabled;

CREATE TABLE ai_engine_rows (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    engine_id         bigint NOT NULL REFERENCES ai_engines(id) ON DELETE CASCADE,
    source_lang       text NOT NULL,
    target_langs      text[] NOT NULL DEFAULT '{}',
    system_prompt_id  bigint NOT NULL REFERENCES ai_prompts(id) ON DELETE RESTRICT,
    segment_prompt_id bigint REFERENCES segment_prompts(id) ON DELETE SET NULL,
    bad_output_action text,
    sort_order        integer NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_engine_rows_engine ON ai_engine_rows (engine_id);
CREATE INDEX idx_ai_engine_rows_system_prompt ON ai_engine_rows (system_prompt_id);

-- +goose Down
DROP TABLE ai_engine_rows;
ALTER TABLE ai_engines
    ADD COLUMN engine_type text NOT NULL DEFAULT 'qe',
    ADD COLUMN prompt_id bigint REFERENCES ai_prompts(id) ON DELETE RESTRICT,
    ADD COLUMN provider text,
    ADD COLUMN bad_output_action text,
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;
