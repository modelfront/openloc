-- +goose Up
-- +goose StatementBegin

-- Companion to 20260908133337 (which promoted human steps): promote the HYBRID
-- and AI step names orgs already use on projects/templates into the library
-- too, so the Steps page is complete and those rows stop reading as "(custom)"
--
-- A non-human step's identity includes its QE+APE engine, so the definition
-- carries the same step_type and ai_system_id. The library is unique on
-- (tenant, lower(name)), so a name gets at most ONE definition, and a name
-- already present — including one the human migration created — is skipped: a
-- "Review" that exists human stays human, and its hybrid uses stay
-- project-local. Existing rows keep their own snapshot; only the provenance
-- link is filled, and only where the row's full identity matches the new def.

-- 1. Create a definition for each distinct non-human step name not already in
--    the library (live OR deleted). DISTINCT ON picks one representative per
--    name, preferring a row that actually carries an engine.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required, ai_system_id)
SELECT DISTINCT ON (sp.tenant_id, lower(ws.name))
       sp.tenant_id, ws.name, ws.step_type, 'linguist', ws.ai_system_id
FROM workflow_steps ws
JOIN settings_profiles sp ON sp.id = ws.settings_profile_id
WHERE ws.step_type IN ('hybrid', 'ai')
  AND NOT EXISTS (
      SELECT 1 FROM step_definitions sd
      WHERE sd.tenant_id = sp.tenant_id AND lower(sd.name) = lower(ws.name)
  )
ORDER BY sp.tenant_id, lower(ws.name),
         (ws.ai_system_id IS NULL),  -- a row that has an engine first
         ws.step_type, ws.ai_system_id, ws.name  -- deterministic representative
ON CONFLICT (tenant_id, lower(name)) WHERE deleted_at IS NULL DO NOTHING;

-- 2. Link non-human profile steps to the matching definition, requiring the
--    SAME name, type AND engine — a same-named row with a different engine is a
--    different identity and stays "(custom)".
UPDATE workflow_steps ws
SET step_definition_id = sd.id
FROM settings_profiles sp
JOIN step_definitions sd ON sd.tenant_id = sp.tenant_id
WHERE ws.settings_profile_id = sp.id
  AND ws.step_type IN ('hybrid', 'ai')
  AND ws.step_definition_id IS NULL
  AND sd.deleted_at IS NULL
  AND lower(ws.name) = lower(sd.name)
  AND sd.step_type = ws.step_type
  AND sd.ai_system_id IS NOT DISTINCT FROM ws.ai_system_id;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- No-op: additive data backfill; see 20260908133337 for the rationale.
SELECT 1;

-- +goose StatementEnd
