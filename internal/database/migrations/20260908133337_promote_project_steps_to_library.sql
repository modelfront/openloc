-- +goose Up
-- +goose StatementBegin

-- Promote the step names orgs already use on projects/templates into the step
-- definition library, so the Steps page reflects what each org actually runs
-- and those steps stop reading as "(custom)".
--
-- Scope: HUMAN steps only. A human step is faithfully representable as a plain
-- library definition. Hybrid/ai steps carry a QE+APE engine, so they are
-- promoted by the companion migration 20260908135715, which copies the engine.
-- Existing project/template rows keep their own snapshot (name, type, engine)
-- either way; only the provenance link (step_definition_id) is filled.

-- 1. Create a human definition for each distinct human step name in use that is
--    NOT already a definition of that tenant — live OR deleted. A deleted one
--    means the org removed it on purpose, so we don't resurrect it. DISTINCT ON
--    collapses case/duplicate names to one definition per tenant.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required)
SELECT DISTINCT ON (sp.tenant_id, lower(ws.name))
       sp.tenant_id, ws.name, 'human', 'linguist'
FROM workflow_steps ws
JOIN settings_profiles sp ON sp.id = ws.settings_profile_id
WHERE ws.step_type = 'human'
  AND NOT EXISTS (
      SELECT 1 FROM step_definitions sd
      WHERE sd.tenant_id = sp.tenant_id AND lower(sd.name) = lower(ws.name)
  )
ORDER BY sp.tenant_id, lower(ws.name), ws.name
ON CONFLICT (tenant_id, lower(name)) WHERE deleted_at IS NULL DO NOTHING;

-- 2. Link the human profile steps to the matching live definition by name (the
--    same rule migration 20260904105035 used for the Translation/Review
--    defaults), so a promoted step reads as its library step rather than
--    "(custom)". Hybrid/ai rows are left untouched.
UPDATE workflow_steps ws
SET step_definition_id = sd.id
FROM settings_profiles sp
JOIN step_definitions sd ON sd.tenant_id = sp.tenant_id
WHERE ws.settings_profile_id = sp.id
  AND ws.step_type = 'human'
  AND ws.step_definition_id IS NULL
  AND sd.deleted_at IS NULL
  AND lower(ws.name) = lower(sd.name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- No-op: this is an additive data backfill. After the fact the promoted
-- definitions are indistinguishable from org-authored ones (an org may have
-- edited them, or composed a workflow from them — the RESTRICT FK would then
-- block a delete anyway), and unlinking would also revert links the original
-- library migration made. Reversing is unsafe, so down leaves the library as
-- it is.
SELECT 1;

-- +goose StatementEnd
