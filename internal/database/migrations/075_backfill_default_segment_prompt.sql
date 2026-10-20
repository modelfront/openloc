-- +goose Up
-- Give every org that has no segment prompt yet the default one (new orgs get it
-- on creation; this backfills pre-existing tenants). Text mirrors
-- segmentprompt.DefaultName / DefaultPromptText — keep them in sync.
INSERT INTO segment_prompts (tenant_id, name, prompt_text)
SELECT t.id,
       'Default segment prompt',
       E'Source: <source_text>\nCurrent translation: <target_text>\nTM suggestion: <tm_match>\nNotes: <note>\nComments: <comments>'
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM segment_prompts sp WHERE sp.tenant_id = t.id
);

-- +goose Down
-- Intentionally a no-op: once backfilled, a default segment prompt is
-- indistinguishable from a user-created one, so we don't delete on rollback.
SELECT 1;
