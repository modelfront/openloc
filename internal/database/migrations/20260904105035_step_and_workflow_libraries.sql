-- +goose Up
-- +goose StatementBegin

-- Step definitions library: the only place a workflow step is created. A
-- step's identity (name, type, engine, role) lives here; per-position fields
-- (assignee, deadline) live on the workflow / profile step rows that use it.
--   human  — a linguist works the step, no AI
--   hybrid — AI runs on step start, a human reviews and completes
--   ai     — AI runs on step start and the step completes itself
CREATE TABLE step_definitions (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name          text NOT NULL,
    step_type     text NOT NULL CHECK (step_type IN ('human', 'hybrid', 'ai')),
    role_required text NOT NULL DEFAULT 'linguist',
    ai_system_id  bigint REFERENCES ai_systems(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    -- hybrid/ai steps need an engine; human steps must not carry one. The
    -- engine FK is SET NULL on engine delete, so the check is on step_type
    -- only when the row is written — a deleted engine leaves a hybrid/ai row
    -- with a NULL engine, which the UI flags as stale.
    CONSTRAINT step_definitions_human_no_engine CHECK (step_type <> 'human' OR ai_system_id IS NULL)
);

CREATE UNIQUE INDEX idx_step_definitions_tenant_name
    ON step_definitions (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE INDEX idx_step_definitions_tenant
    ON step_definitions (tenant_id) WHERE deleted_at IS NULL;

-- Workflow library: a named, ordered list of step definitions. Projects and
-- templates COPY a workflow's steps into their own workflow_steps rows and
-- keep a provenance pointer; editing a library workflow never touches them.
CREATE TABLE workflows (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE UNIQUE INDEX idx_workflows_tenant_name
    ON workflows (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE INDEX idx_workflows_tenant
    ON workflows (tenant_id) WHERE deleted_at IS NULL;

CREATE TABLE workflow_definition_steps (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_id        bigint NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    step_definition_id bigint NOT NULL REFERENCES step_definitions(id) ON DELETE RESTRICT,
    step_order         integer NOT NULL,
    default_assignee   bigint REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (workflow_id, step_order)
);

CREATE INDEX idx_workflow_definition_steps_definition
    ON workflow_definition_steps (step_definition_id);

-- Profile step rows snapshot the definition's identity so a running project
-- is immune to library edits; step_definition_id is provenance only.
ALTER TABLE workflow_steps
    ADD COLUMN step_type text NOT NULL DEFAULT 'human' CHECK (step_type IN ('human', 'hybrid', 'ai')),
    ADD COLUMN step_definition_id bigint REFERENCES step_definitions(id) ON DELETE SET NULL;

UPDATE workflow_steps SET step_type = 'hybrid' WHERE ai_system_id IS NOT NULL;

-- Which library workflow a profile's steps were copied from ("Based on …").
ALTER TABLE settings_profiles
    ADD COLUMN workflow_id bigint REFERENCES workflows(id) ON DELETE SET NULL;

-- Every tenant gets the two default step definitions, and existing human
-- steps with a matching name are linked to them.
INSERT INTO step_definitions (tenant_id, name, step_type, role_required)
SELECT t.id, d.name, 'human', 'linguist'
FROM tenants t
CROSS JOIN (VALUES ('Translation'), ('Review')) AS d(name);

UPDATE workflow_steps ws
SET step_definition_id = sd.id
FROM settings_profiles sp
JOIN step_definitions sd ON sd.tenant_id = sp.tenant_id
WHERE ws.settings_profile_id = sp.id
  AND ws.step_type = 'human'
  AND ws.step_definition_id IS NULL
  AND lower(ws.name) = lower(sd.name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE settings_profiles DROP COLUMN workflow_id;
ALTER TABLE workflow_steps DROP COLUMN step_definition_id;
ALTER TABLE workflow_steps DROP COLUMN step_type;
DROP TABLE workflow_definition_steps;
DROP TABLE workflows;
DROP TABLE step_definitions;

-- +goose StatementEnd
