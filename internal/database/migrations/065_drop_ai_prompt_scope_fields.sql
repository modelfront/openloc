-- +goose Up
-- Drop the AI-prompt scoping metadata that was never wired to anything. When
-- 057 introduced ai_prompts it added source_language / target_language / step /
-- customer_id / domain as "optional scoping metadata … Config only — nothing
-- executes these yet." Nothing ever came to execute them: prompt/engine
-- selection is by explicit workflow-step assignment, and only `provider` +
-- `prompt_text` are read at runtime. The columns were pure write-only UI noise,
-- so they go. `provider` stays (it IS consumed).
ALTER TABLE ai_prompts
    DROP COLUMN source_language,
    DROP COLUMN target_language,
    DROP COLUMN step,
    DROP COLUMN customer_id,
    DROP COLUMN domain;

-- +goose Down
ALTER TABLE ai_prompts
    ADD COLUMN source_language text,
    ADD COLUMN target_language text,
    ADD COLUMN step            text,
    ADD COLUMN customer_id     bigint REFERENCES customers(id) ON DELETE SET NULL,
    ADD COLUMN domain          text;
