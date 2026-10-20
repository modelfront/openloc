-- +goose Up
-- The language pair now lives in the prompt's own instruction text, named with
-- the <source_lang> / <target_lang> tokens the runtime substitutes.
--
-- It used to be a sentence the platform appended after the instruction, which
-- the author could neither see nor edit. Dropping that sentence without moving
-- it would leave every existing prompt silently never telling the model which
-- languages it is working in — so the sentence is written into the text here,
-- where it is visible in the editor and can be reworded or removed.
--
-- Only custom prompts. The built-ins carry the tokens in code and are refreshed
-- by UpsertDefaultAIPrompt on the next list.
--
-- Skips any prompt that already names the pair: a tenant who wrote the tokens
-- in by hand (they were offered in the editor long before anything substituted
-- them) must not get the sentence twice.
UPDATE ai_prompts
SET prompt_text = prompt_text || E'\n\n' || CASE
        -- MT is the only mode that emits text with no status and no comment
        -- (flagsToMode / aiprompt.ModeForFlags).
        WHEN out_text AND NOT out_status AND NOT out_comments
            THEN 'Translate from <source_lang> to <target_lang>.'
        ELSE 'You are evaluating a machine translation from <source_lang> to <target_lang>.'
    END,
    updated_at = now()
WHERE NOT is_default
  AND prompt_text NOT LIKE '%<source_lang>%'
  AND prompt_text NOT LIKE '%<target_lang>%';

-- +goose Down
-- Strip the appended sentence again, leaving any tenant-authored use of the
-- tokens alone — only the exact text this migration added is removed.
UPDATE ai_prompts
SET prompt_text = rtrim(
        replace(
            replace(prompt_text,
                E'\n\nTranslate from <source_lang> to <target_lang>.', ''),
            E'\n\nYou are evaluating a machine translation from <source_lang> to <target_lang>.', '')
    ),
    updated_at = now()
WHERE NOT is_default;
