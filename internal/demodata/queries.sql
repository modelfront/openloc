-- Demo workspace manifest (demo_workspace_items). Every query is tenant-scoped.

-- name: LockDemoWorkspace :exec
-- Serialises demodata.Create per tenant for the rest of the transaction, so two
-- concurrent calls cannot both pass the "already present" check.
SELECT pg_advisory_xact_lock(hashtextextended('demo_workspace:' || sqlc.arg(tenant_id)::bigint::text, 0));

-- name: DemoWorkspacePresent :one
-- True while any demo project still exists in the tenant. The name check also
-- covers databases seeded before the manifest existed.
SELECT (EXISTS (
    SELECT 1 FROM demo_workspace_items d
    JOIN projects p ON p.id = d.entity_id AND p.tenant_id = d.tenant_id AND p.deleted_at IS NULL
    WHERE d.tenant_id = sqlc.arg(tenant_id) AND d.entity_type = 'project'
) OR EXISTS (
    SELECT 1 FROM projects
    WHERE tenant_id = sqlc.arg(tenant_id) AND name = sqlc.arg(marker_project_name)::text
      AND deleted_at IS NULL
))::boolean AS present;

-- name: RecordDemoWorkspaceItem :exec
INSERT INTO demo_workspace_items (tenant_id, entity_type, entity_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListDemoWorkspaceItems :many
SELECT entity_type, entity_id, created_at FROM demo_workspace_items
WHERE tenant_id = $1
ORDER BY entity_type, entity_id;
