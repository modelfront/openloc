-- Project labels: tenant-defined label names ("Country") with a fixed set of
-- allowed values ("Germany"), and at most one value per label per project.
-- Definitions and values are soft-deletable, so every read filters
-- `deleted_at IS NULL`. Values carry no tenant_id of their own — they are
-- tenant-scoped by joining through label_definitions.

-- name: ListLabelDefinitions :many
-- Built-ins first (Customer, then Domain), then the tenant's own by name.
-- `system_key IS NULL` sorts false before true, which puts the built-ins on top.
SELECT * FROM label_definitions
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY (system_key IS NULL), lower(name);

-- name: GetLabelDefinition :one
SELECT * FROM label_definitions
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateLabelDefinition :one
INSERT INTO label_definitions (tenant_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: SoftDeleteLabelDefinition :execrows
-- Refuses while any project still carries one of the label's live values. The
-- NOT EXISTS is part of the UPDATE rather than a separate count so the check
-- and the delete cannot be separated by a project picking the label in
-- between. 0 rows means "not found, already gone, or in use" — the handler
-- counts afterwards only to pick which of those to say. A pick on a deleted
-- value does not count: it is hidden everywhere already.
UPDATE label_definitions d
SET deleted_at = now(), updated_at = now()
WHERE d.id = $1 AND d.tenant_id = $2 AND d.deleted_at IS NULL
  AND d.system_key IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM project_labels pl
    JOIN label_values v ON v.id = pl.label_value_id AND v.deleted_at IS NULL
    WHERE pl.label_definition_id = d.id
  );

-- name: ListLabelValues :many
-- Every live value of every live label in the tenant, so the labels list costs
-- two round trips rather than one per label.
SELECT v.* FROM label_values v
JOIN label_definitions d ON d.id = v.label_definition_id AND d.deleted_at IS NULL
WHERE d.tenant_id = $1 AND v.deleted_at IS NULL
ORDER BY lower(v.value);

-- name: GetLabelValue :one
SELECT v.* FROM label_values v
JOIN label_definitions d ON d.id = v.label_definition_id AND d.deleted_at IS NULL
WHERE v.id = $1 AND d.tenant_id = $2 AND v.deleted_at IS NULL;

-- name: CreateLabelValue :one
INSERT INTO label_values (label_definition_id, value)
VALUES ($1, $2)
RETURNING *;

-- name: SoftDeleteLabelValue :execrows
-- No in-use guard: the projects and templates that picked the value lose the
-- pick (DeleteProjectLabelsForValue / DeleteTemplateLabelsForValue, same
-- transaction).
UPDATE label_values v
SET deleted_at = now(), updated_at = now()
FROM label_definitions d
WHERE v.id = $1 AND d.id = v.label_definition_id
  AND d.tenant_id = $2 AND v.deleted_at IS NULL;

-- name: DeleteProjectLabelsForValue :exec
-- Unsets a deleted value on every project of the tenant that picked it. Link
-- rows, so a hard delete.
DELETE FROM project_labels pl
USING projects p
WHERE pl.label_value_id = $1 AND p.id = pl.project_id AND p.tenant_id = $2;

-- name: DeleteTemplateLabelsForValue :exec
-- Same for the templates that preset it.
DELETE FROM template_labels tl
USING templates t
WHERE tl.label_value_id = $1 AND t.id = tl.template_id AND t.tenant_id = $2;

-- name: CountProjectsForLabel :one
-- How many projects carry one of this label's live values.
SELECT count(*) FROM project_labels pl
JOIN projects p ON p.id = pl.project_id
JOIN label_values v ON v.id = pl.label_value_id AND v.deleted_at IS NULL
WHERE pl.label_definition_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: ListLabelValueUsage :many
-- How many projects use each live value, so the list marks values in use and
-- the UI can say why a label cannot be deleted yet.
SELECT pl.label_value_id, count(*) AS project_count
FROM project_labels pl
JOIN projects p ON p.id = pl.project_id
JOIN label_values v ON v.id = pl.label_value_id AND v.deleted_at IS NULL
WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
GROUP BY pl.label_value_id;

-- name: CountProjectsForLabelValue :one
-- Feeds the "N projects use this value" warning before a value is deleted.
SELECT count(*) FROM project_labels pl
JOIN projects p ON p.id = pl.project_id
WHERE pl.label_value_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL;

-- name: ListProjectLabels :many
-- A project's picks, joined out to the names the UI renders. Soft-deleted
-- definitions and values drop out here, so retiring one hides it everywhere
-- without touching the project rows that reference it.
SELECT pl.label_definition_id, pl.label_value_id,
       d.name AS label_name, v.value AS label_value
FROM project_labels pl
JOIN projects p ON p.id = pl.project_id
JOIN label_definitions d ON d.id = pl.label_definition_id AND d.deleted_at IS NULL
JOIN label_values v ON v.id = pl.label_value_id AND v.deleted_at IS NULL
WHERE pl.project_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL
ORDER BY lower(d.name);

-- name: SetProjectLabel :exec
-- One value per label per project: the primary key makes a second pick an
-- update, not a duplicate row.
INSERT INTO project_labels (project_id, label_definition_id, label_value_id)
VALUES ($1, $2, $3)
ON CONFLICT (project_id, label_definition_id)
DO UPDATE SET label_value_id = EXCLUDED.label_value_id;

-- name: DeleteProjectLabel :exec
-- Link row, so this stays a hard delete.
DELETE FROM project_labels
WHERE project_id = $1 AND label_definition_id = $2;

-- name: DeleteProjectLabels :exec
-- Clears every pick, for the "replace the whole set" path.
DELETE FROM project_labels WHERE project_id = $1;

-- name: GetProjectSystemLabelValue :one
-- The value a project carries for a built-in label (customer, domain), which is
-- where those two are stored. Empty result = the project has no pick.
SELECT v.id, v.value
FROM project_labels pl
JOIN projects p ON p.id = pl.project_id
JOIN label_definitions d ON d.id = pl.label_definition_id AND d.deleted_at IS NULL
JOIN label_values v ON v.id = pl.label_value_id AND v.deleted_at IS NULL
WHERE pl.project_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL AND d.system_key = $3;

-- name: ListTemplateLabels :many
-- A template's presets, joined out to the names the UI renders. Same shape as
-- ListProjectLabels; soft-deleted definitions and values drop out here too.
SELECT tl.label_definition_id, tl.label_value_id,
       d.name AS label_name, v.value AS label_value
FROM template_labels tl
JOIN templates t ON t.id = tl.template_id
JOIN label_definitions d ON d.id = tl.label_definition_id AND d.deleted_at IS NULL
JOIN label_values v ON v.id = tl.label_value_id AND v.deleted_at IS NULL
WHERE tl.template_id = $1 AND t.tenant_id = $2
ORDER BY lower(d.name);

-- name: SetTemplateLabel :exec
INSERT INTO template_labels (template_id, label_definition_id, label_value_id)
VALUES ($1, $2, $3)
ON CONFLICT (template_id, label_definition_id)
DO UPDATE SET label_value_id = EXCLUDED.label_value_id;

-- name: DeleteTemplateLabels :exec
DELETE FROM template_labels WHERE template_id = $1;
