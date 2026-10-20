-- +goose Up
-- Per-language additions to a system prompt. A prompt is reused across every
-- language pair an engine routes, so advice that only holds for one language
-- ("in German, address the reader as Sie") had nowhere to live: either it went
-- into the shared instruction text and reached jobs it does not apply to, or the
-- tenant duplicated the whole prompt per language.
--
-- An addition constrains the source side, the target side, or both. An empty
-- list means "any" for that side, so source=[] target=[de] is "any job into
-- German" and source=[en] target=[de] is "English into German only". The CHECK
-- keeps at least one side constrained: an addition with neither would match
-- every job, which is what the prompt's own text is for.
--
-- Matching is by base subtag in Go (langcode.SameBase), so `de` covers a de-AT
-- job; the codes are stored as written after normalisation.
CREATE TABLE ai_prompt_language_additions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    prompt_id        bigint NOT NULL REFERENCES ai_prompts(id) ON DELETE CASCADE,
    source_languages text[] NOT NULL DEFAULT '{}',
    target_languages text[] NOT NULL DEFAULT '{}',
    addition_text    text NOT NULL,
    -- Append order, so a tenant controls which addition the model reads first.
    position         int NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ai_prompt_language_additions_needs_a_side CHECK (
        cardinality(source_languages) > 0 OR cardinality(target_languages) > 0
    )
);

CREATE INDEX idx_ai_prompt_lang_additions_prompt
    ON ai_prompt_language_additions (prompt_id, position);

-- +goose Down
DROP TABLE ai_prompt_language_additions;
