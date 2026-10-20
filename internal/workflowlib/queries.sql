-- Workflow library: named, ordered lists of step
-- definitions. Tenant-scoped, soft-deleted. Nothing references
-- workflow_definition_steps rows, so a workflow edit replaces them wholesale.
-- Built-in workflows (system_key set) are not library rows: the library
-- queries skip them, so they cannot be listed, edited or deleted. Only
-- GetApplicableWorkflow, GetSystemWorkflow and the provenance lookup see them.

-- name: ListWorkflows :many
SELECT * FROM workflows
WHERE tenant_id = $1 AND deleted_at IS NULL AND system_key IS NULL
ORDER BY lower(name);

-- name: GetWorkflowByName :one
-- Live workflow of a tenant by name (case-insensitive). Used for idempotent
-- default-workflow provisioning of a new org.
SELECT * FROM workflows
WHERE tenant_id = @tenant_id AND lower(name) = lower(@name) AND deleted_at IS NULL AND system_key IS NULL;

-- name: GetWorkflow :one
SELECT * FROM workflows
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL;

-- name: GetApplicableWorkflow :one
-- A live workflow a project or template may copy its steps from: a library
-- row or a built-in one.
SELECT * FROM workflows
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: GetSystemWorkflow :one
SELECT * FROM workflows
WHERE tenant_id = $1 AND system_key = $2 AND deleted_at IS NULL;

-- name: EnsureSystemWorkflow :exec
-- Idempotent insert of a built-in workflow at tenant provisioning.
INSERT INTO workflows (tenant_id, name, system_key)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, system_key) WHERE system_key IS NOT NULL AND deleted_at IS NULL DO NOTHING;

-- name: GetWorkflowAny :one
-- Resolves a workflow regardless of deleted_at ("Based on …" provenance).
SELECT * FROM workflows
WHERE id = $1 AND tenant_id = $2;

-- name: CreateWorkflow :one
INSERT INTO workflows (tenant_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: UpdateWorkflowName :one
UPDATE workflows
SET name = $3, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL
RETURNING *;

-- name: TouchWorkflow :exec
UPDATE workflows SET updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL;

-- name: SoftDeleteWorkflow :exec
UPDATE workflows
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND system_key IS NULL;

-- name: ListWorkflowDefinitionSteps :many
-- Ordered steps of one workflow with the definition's identity fields and the
-- assignee's display name. Tenant-scoped through the workflow join.
SELECT wds.id, wds.workflow_id, wds.step_order, wds.default_assignee,
       wds.step_definition_id,
       sd.name, sd.step_type, sd.role_required, sd.ai_router_id,
       (sd.deleted_at IS NOT NULL)::bool AS definition_deleted,
       u.name AS assignee_name
FROM workflow_definition_steps wds
JOIN workflows w ON w.id = wds.workflow_id
JOIN step_definitions sd ON sd.id = wds.step_definition_id
LEFT JOIN users u ON u.id = wds.default_assignee
WHERE wds.workflow_id = $1 AND w.tenant_id = $2
ORDER BY wds.step_order;

-- name: ListWorkflowDefinitionStepsForTenant :many
-- Every live workflow's steps in one round trip (list page chips): identity,
-- the position's assignee, and the definition's AI router.
SELECT wds.workflow_id, wds.step_order, wds.step_definition_id,
       sd.name, sd.step_type, sd.ai_router_id,
       rt.name AS ai_router_name,
       wds.default_assignee, u.name AS assignee_name
FROM workflow_definition_steps wds
JOIN workflows w ON w.id = wds.workflow_id
JOIN step_definitions sd ON sd.id = wds.step_definition_id
LEFT JOIN ai_routers rt ON rt.id = sd.ai_router_id
LEFT JOIN users u ON u.id = wds.default_assignee
WHERE w.tenant_id = $1 AND w.deleted_at IS NULL AND w.system_key IS NULL
ORDER BY wds.workflow_id, wds.step_order;

-- name: DeleteWorkflowDefinitionSteps :exec
DELETE FROM workflow_definition_steps WHERE workflow_id = $1;

-- name: CreateWorkflowDefinitionStep :one
INSERT INTO workflow_definition_steps (workflow_id, step_definition_id, step_order, default_assignee)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountProfilesFromWorkflow :one
-- Projects and templates whose steps were copied from a library workflow.
-- They keep their own copy, so an edit or delete of the workflow does not
-- change them; the count tells the caller how far the provenance reaches.
SELECT
    (SELECT count(*) FROM projects p
       JOIN settings_profiles sp ON sp.id = p.settings_profile_id
      WHERE sp.workflow_id = @workflow_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL)::bigint AS projects,
    (SELECT count(*) FROM templates t
       JOIN settings_profiles sp ON sp.id = t.settings_profile_id
      WHERE sp.workflow_id = @workflow_id AND t.tenant_id = @tenant_id)::bigint AS templates;
