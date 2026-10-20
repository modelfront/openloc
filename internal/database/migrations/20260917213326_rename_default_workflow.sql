-- The workflow every org is provisioned with is called "Default";
-- it was "Translate and Review"
-- (20260908115706_default_workflow_per_tenant.sql).
-- +goose Up
-- +goose StatementBegin

-- Only the untouched seeded one: a tenant that already has a workflow called
-- "Default" keeps both, since the rename would collide with its own.
UPDATE workflows w
SET name = 'Default', updated_at = now()
WHERE lower(w.name) = lower('Translate and Review')
  AND w.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM workflows other
      WHERE other.tenant_id = w.tenant_id
        AND lower(other.name) = lower('Default')
        AND other.deleted_at IS NULL
  );

-- Tenants created since the backfill (or whose seeded workflow was deleted)
-- get one now, composed of their Translation → Review steps.
INSERT INTO workflows (tenant_id, name)
SELECT t.id, 'Default'
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1 FROM workflows w
    WHERE w.tenant_id = t.id
      AND lower(w.name) = lower('Default')
      AND w.deleted_at IS NULL
);

INSERT INTO workflow_definition_steps (workflow_id, step_definition_id, step_order)
SELECT w.id, sd.id, ord.step_order
FROM workflows w
JOIN (VALUES ('Translation', 0), ('Review', 1)) AS ord(name, step_order) ON TRUE
JOIN step_definitions sd
    ON sd.tenant_id = w.tenant_id
   AND lower(sd.name) = lower(ord.name)
   AND sd.deleted_at IS NULL
WHERE lower(w.name) = lower('Default')
  AND w.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM workflow_definition_steps wds WHERE wds.workflow_id = w.id
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE workflows w
SET name = 'Translate and Review', updated_at = now()
WHERE lower(w.name) = lower('Default')
  AND w.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM workflows other
      WHERE other.tenant_id = w.tenant_id
        AND lower(other.name) = lower('Translate and Review')
        AND other.deleted_at IS NULL
  );

-- +goose StatementEnd
