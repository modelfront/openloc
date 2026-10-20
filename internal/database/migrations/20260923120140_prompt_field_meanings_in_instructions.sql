-- +goose Up
-- What each reply field MEANS now lives in the prompt's own instruction text,
-- where the author can read and reword it. The platform still owns the SHAPE:
-- the JSON schema names the fields and their types, and the segment-context
-- checkboxes decide which keys a segment carries.
--
-- Until now the meanings were appended at send time and never shown. Dropping
-- them without moving them would leave every existing prompt describing no
-- field at all, so they are written into the text here.
--
-- Only custom prompts. The built-ins carry the wording in code and are
-- refreshed by UpsertDefaultAIPrompt on the next list.
--
-- Skips any prompt that already names one of its own reply fields — a tenant
-- who wrote the meanings in by hand must not get them twice.
UPDATE ai_prompts
SET prompt_text = prompt_text || E'\n\n' || CASE
        -- Mode is derived from the output flags, exactly as aiprompt.ModeForFlags
        -- does it: status+text is QE+APE, status alone is QE, comments without
        -- status is APE, and the rest is MT.
        WHEN out_status AND out_text THEN
            'For each segment return "postedit" with a minimally corrected translation when the original has fixable errors, or null when it is fine as-is or beyond repair. "approved" and "score" judge the ORIGINAL translation; "postedit_approved" and "postedit_score" judge YOUR postedit and are null when you provide none. "comment" is a short remark for the human reviewer, or null.'
        WHEN out_status THEN
            'For each segment return "approved" true only when the translation is publication-ready as-is.'
        WHEN out_comments THEN
            'For each segment return "postedit" with a minimally corrected translation, or null when the translation is fine as-is or beyond repair. Do not judge whether the segment is ready to publish and do not score it — a human reviews every one. "comment" is a short remark for the human reviewer, or null.'
        ELSE
            'For each segment return "target" with the translation.'
    END,
    updated_at = now()
WHERE NOT is_default
  AND prompt_text NOT LIKE '%"target"%'
  AND prompt_text NOT LIKE '%"approved"%'
  AND prompt_text NOT LIKE '%"postedit"%';

-- +goose Down
-- Removes only the sentences the Up appended, leaving anything the tenant wrote
-- after them. A prompt whose text was edited in between keeps that edit; the
-- appended block is matched literally or not at all.
UPDATE ai_prompts
SET prompt_text = rtrim(
        replace(prompt_text,
            E'\n\n' || CASE
                WHEN out_status AND out_text THEN
                    'For each segment return "postedit" with a minimally corrected translation when the original has fixable errors, or null when it is fine as-is or beyond repair. "approved" and "score" judge the ORIGINAL translation; "postedit_approved" and "postedit_score" judge YOUR postedit and are null when you provide none. "comment" is a short remark for the human reviewer, or null.'
                WHEN out_status THEN
                    'For each segment return "approved" true only when the translation is publication-ready as-is.'
                WHEN out_comments THEN
                    'For each segment return "postedit" with a minimally corrected translation, or null when the translation is fine as-is or beyond repair. Do not judge whether the segment is ready to publish and do not score it — a human reviews every one. "comment" is a short remark for the human reviewer, or null.'
                ELSE
                    'For each segment return "target" with the translation.'
            END, ''),
        E' \n')
WHERE NOT is_default;
