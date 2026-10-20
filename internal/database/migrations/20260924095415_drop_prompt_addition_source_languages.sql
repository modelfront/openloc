-- +goose Up
-- A prompt addition constrains the target side only. A router picks its engine
-- by target language, and a buyer translates out of one source, so the source
-- constraint was a picker that never changed the outcome.
--
-- An addition that named only a source has nothing left to constrain — it would
-- match every job, which is what the prompt's own text is for — so those rows go
-- before the CHECK is rewritten. Verify the count on a database before running:
--   SELECT count(*) FROM ai_prompt_language_additions WHERE cardinality(target_languages) = 0;
DELETE FROM ai_prompt_language_additions WHERE cardinality(target_languages) = 0;

ALTER TABLE ai_prompt_language_additions
    DROP CONSTRAINT ai_prompt_language_additions_needs_a_side,
    DROP COLUMN source_languages,
    ADD CONSTRAINT ai_prompt_language_additions_needs_a_target CHECK (
        cardinality(target_languages) > 0
    );

-- +goose Down
ALTER TABLE ai_prompt_language_additions
    DROP CONSTRAINT ai_prompt_language_additions_needs_a_target,
    ADD COLUMN source_languages text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT ai_prompt_language_additions_needs_a_side CHECK (
        cardinality(source_languages) > 0 OR cardinality(target_languages) > 0
    );
