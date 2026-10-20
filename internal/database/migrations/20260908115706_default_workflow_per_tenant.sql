-- +goose Up
-- +goose StatementBegin

-- Every org starts with a "Translate and Review" workflow, composed of two
-- human steps (Translation → Review), so the Workflows and Steps pages are
-- never empty. This backfills existing tenants; new
-- tenants get the same via provisioning (workflowlib.EnsureDefaultWorkflow,
-- called from the org-signup / oauth / platform paths).

-- 1. Ensure the two default human step definitions exist for every tenant. The
--    original library migration created them for tenants that existed then;
--    this covers any created since. Idempotent on the partial unique index, so
--    a tenant that renamed or deleted a default is left alone.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required)
SELECT t.id, d.name, 'human', 'linguist'
FROM tenants t
CROSS JOIN (VALUES ('Translation'), ('Review')) AS d(name)
ON CONFLICT (tenant_id, lower(name)) WHERE deleted_at IS NULL DO NOTHING;

-- 2. Create the default workflow for every tenant that has no live workflow by
--    that name, so a tenant that already made its own "Translate and Review"
--    is left untouched.
INSERT INTO workflows (tenant_id, name)
SELECT t.id, 'Translate and Review'
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflows w
    WHERE w.tenant_id = t.id
      AND lower(w.name) = lower('Translate and Review')
      AND w.deleted_at IS NULL
);

-- 3. Compose each empty "Translate and Review" workflow from the tenant's
--    Translation → Review definitions. The NOT EXISTS guard fills only
--    workflows with no steps yet, so a re-run leaves composed ones alone.
INSERT INTO workflow_definition_steps (workflow_id, step_definition_id, step_order)
SELECT w.id, sd.id, ord.step_order
FROM workflows w
JOIN (VALUES ('Translation', 0), ('Review', 1)) AS ord(name, step_order) ON TRUE
JOIN step_definitions sd
    ON sd.tenant_id = w.tenant_id
   AND lower(sd.name) = lower(ord.name)
   AND sd.deleted_at IS NULL
WHERE lower(w.name) = lower('Translate and Review')
  AND w.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM workflow_definition_steps wds WHERE wds.workflow_id = w.id
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop the seeded workflow (its composition rows cascade). The step
-- definitions are shared with the lazy defaults, so they stay.
DELETE FROM workflows WHERE lower(name) = lower('Translate and Review');

-- +goose StatementEnd
