-- +goose Up
-- `note` and `comments` are no longer per-segment context a prompt can send.
-- Both described a TRANSLATION, so on an MT prompt they were empty on every
-- segment, and elsewhere they were rarely filled — a checkbox that bought a
-- query and usually nothing else. Only the two source-side fields remain:
-- tm_match and terms.
--
-- The runtime already ignores them (segmentprompt.FieldsFor drops any key it
-- does not know), so this is not what makes them stop being sent. It is here so
-- the stored value matches what the prompt page shows: leaving them behind
-- means a row that claims to send a note forever, and the next person to read
-- the column has to know which keys are live.
UPDATE ai_prompts
SET segment_context = array_remove(array_remove(segment_context, 'note'), 'comments'),
    updated_at = now()
WHERE segment_context && ARRAY['note', 'comments'];

-- +goose Down
-- Deliberately empty. The Down cannot know which prompts had which key — the
-- rows are gone, and re-adding both to every prompt that now has neither would
-- invent settings nobody chose. Rolling back the code restores the checkboxes;
-- the tenant ticks the ones they want again.
SELECT 1;
