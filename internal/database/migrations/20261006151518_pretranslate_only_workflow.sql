-- +goose Up
-- +goose StatementBegin

-- `pretranslate` is a fourth, automated step type: pre-translation runs when
-- the step starts and the step completes itself afterwards. It carries no
-- router and no deadline, and only the built-in step definition uses it.
ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_step_type_check;
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_step_type_check
    CHECK (step_type IN ('human', 'hybrid', 'ai', 'pretranslate'));
ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_human_no_router;
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_human_no_router
    CHECK (step_type NOT IN ('human', 'pretranslate') OR ai_router_id IS NULL);

ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_step_type_check;
ALTER TABLE workflow_steps
    ADD CONSTRAINT workflow_steps_step_type_check
    CHECK (step_type IN ('human', 'hybrid', 'ai', 'pretranslate'));
ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_ai_no_deadline;
ALTER TABLE workflow_steps
    ADD CONSTRAINT workflow_steps_ai_no_deadline
    CHECK (step_type NOT IN ('ai', 'pretranslate') OR deadline IS NULL);

-- Built-in rows carry a system_key. They are hidden from the library lists and
-- cannot be renamed, edited or deleted. Names stay unique among the tenant's
-- own rows only, so a tenant step already called "Pre-translation" is fine.
ALTER TABLE step_definitions ADD COLUMN system_key text;
ALTER TABLE workflows ADD COLUMN system_key text;

CREATE UNIQUE INDEX idx_step_definitions_tenant_system_key
    ON step_definitions (tenant_id, system_key)
    WHERE system_key IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX idx_workflows_tenant_system_key
    ON workflows (tenant_id, system_key)
    WHERE system_key IS NOT NULL AND deleted_at IS NULL;

DROP INDEX idx_step_definitions_tenant_name;
CREATE UNIQUE INDEX idx_step_definitions_tenant_name
    ON step_definitions (tenant_id, lower(name))
    WHERE deleted_at IS NULL AND system_key IS NULL;
DROP INDEX idx_workflows_tenant_name;
CREATE UNIQUE INDEX idx_workflows_tenant_name
    ON workflows (tenant_id, lower(name))
    WHERE deleted_at IS NULL AND system_key IS NULL;

-- Every existing tenant gets the built-in step and workflow. New tenants get
-- them at provisioning (systemworkflow.EnsurePretranslate).
INSERT INTO step_definitions (tenant_id, name, step_type, role_required, system_key)
SELECT t.id, 'Pre-translation', 'pretranslate', 'linguist', 'pretranslate'
FROM tenants t
ON CONFLICT (tenant_id, system_key) WHERE system_key IS NOT NULL AND deleted_at IS NULL DO NOTHING;

INSERT INTO workflows (tenant_id, name, system_key)
SELECT t.id, 'Pre-translation only', 'pretranslate_only'
FROM tenants t
ON CONFLICT (tenant_id, system_key) WHERE system_key IS NOT NULL AND deleted_at IS NULL DO NOTHING;

INSERT INTO workflow_definition_steps (workflow_id, step_definition_id, step_order)
SELECT w.id, sd.id, 0
FROM workflows w
JOIN step_definitions sd
    ON sd.tenant_id = w.tenant_id
   AND sd.system_key = 'pretranslate'
   AND sd.deleted_at IS NULL
WHERE w.system_key = 'pretranslate_only'
  AND w.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM workflow_definition_steps wds WHERE wds.workflow_id = w.id
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Project and template step rows of the new type fall back to human steps, so
-- the narrower type check holds again; they lose their provenance link.
UPDATE workflow_steps SET step_type = 'human', step_definition_id = NULL
WHERE step_type = 'pretranslate';
UPDATE settings_profiles SET workflow_id = NULL
WHERE workflow_id IN (SELECT id FROM workflows WHERE system_key IS NOT NULL);
DELETE FROM workflows WHERE system_key IS NOT NULL;
DELETE FROM step_definitions WHERE system_key IS NOT NULL;

DROP INDEX idx_workflows_tenant_name;
CREATE UNIQUE INDEX idx_workflows_tenant_name
    ON workflows (tenant_id, lower(name)) WHERE deleted_at IS NULL;
DROP INDEX idx_step_definitions_tenant_name;
CREATE UNIQUE INDEX idx_step_definitions_tenant_name
    ON step_definitions (tenant_id, lower(name)) WHERE deleted_at IS NULL;

DROP INDEX idx_workflows_tenant_system_key;
DROP INDEX idx_step_definitions_tenant_system_key;
ALTER TABLE workflows DROP COLUMN system_key;
ALTER TABLE step_definitions DROP COLUMN system_key;

ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_ai_no_deadline;
ALTER TABLE workflow_steps
    ADD CONSTRAINT workflow_steps_ai_no_deadline CHECK (step_type <> 'ai' OR deadline IS NULL);
ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_step_type_check;
ALTER TABLE workflow_steps
    ADD CONSTRAINT workflow_steps_step_type_check CHECK (step_type IN ('human', 'hybrid', 'ai'));

ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_human_no_router;
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_human_no_router CHECK (step_type <> 'human' OR ai_router_id IS NULL);
ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_step_type_check;
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_step_type_check CHECK (step_type IN ('human', 'hybrid', 'ai'));

-- +goose StatementEnd
