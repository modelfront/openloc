-- +goose Up
-- The built-in segment prompts become JSON objects.
--
-- A segment prompt is the per-segment user message. The built-ins were labelled
-- prose ("Source: <source_text>"), which the model had to parse back into
-- fields; as an object it addresses each value by what it IS. The batch envelope
-- carries the result as a nested object, so nothing gets double-escaped.
--
-- Scoped to rows that still hold the exact seeded text: a tenant who edited
-- their default made a decision, and this migration is not the place to
-- overrule it. Matching on the body rather than the name is deliberate — these
-- rows have carried three different names across migrations, but only one
-- body.

UPDATE segment_prompts
SET prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": "<tm_match>",
  "note": "<note>",
  "comments": "<comments>"
}'
WHERE prompt_text = 'Source: <source_text>
Current translation: <target_text>
TM suggestion: <tm_match>
Notes: <note>
Comments: <comments>';

UPDATE segment_prompts
SET prompt_text = '{
  "source": "<source_text>",
  "tm_match": "<tm_match>"
}'
WHERE prompt_text = 'Source: <source_text>
TM suggestion: <tm_match>';

-- +goose Down
-- Symmetric: only rows still holding the JSON this migration wrote go back.

UPDATE segment_prompts
SET prompt_text = 'Source: <source_text>
Current translation: <target_text>
TM suggestion: <tm_match>
Notes: <note>
Comments: <comments>'
WHERE prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": "<tm_match>",
  "note": "<note>",
  "comments": "<comments>"
}';

UPDATE segment_prompts
SET prompt_text = 'Source: <source_text>
TM suggestion: <tm_match>'
WHERE prompt_text = '{
  "source": "<source_text>",
  "tm_match": "<tm_match>"
}';
