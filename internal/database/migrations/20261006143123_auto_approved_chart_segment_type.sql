-- +goose Up
-- The default "Auto-approved words by segment type" chart groups by
-- segment_type (where the approved text came from). auto_approved counts the
-- approval events, so grouping it by kind only splits auto_approved from ape.
-- Rewrites charts in exactly the default's shape; a chart someone changed
-- keeps its definition.
UPDATE dashboard_charts
SET definition = jsonb_set(definition, '{dimensions}', '["week", "segment_type"]'::jsonb),
    viz        = jsonb_set(viz, '{series_key}', '"segment_type"'::jsonb)
WHERE definition->>'measure' = 'auto_approved'
  AND definition->'dimensions' = '["week", "kind"]'::jsonb
  AND viz->>'series_key' = 'kind';

-- +goose Down
UPDATE dashboard_charts
SET definition = jsonb_set(definition, '{dimensions}', '["week", "kind"]'::jsonb),
    viz        = jsonb_set(viz, '{series_key}', '"kind"'::jsonb)
WHERE definition->>'measure' = 'auto_approved'
  AND definition->'dimensions' = '["week", "segment_type"]'::jsonb
  AND viz->>'series_key' = 'segment_type';
