-- +goose Up
-- +goose StatementBegin

-- Customer and Domain become labels, so a tenant manages
-- all three the same way on one page. They keep a `system_key`, which marks
-- them built in: they cannot be deleted, and code that needs "the Customer
-- label" finds it by key rather than by name (a tenant may rename them).
--
-- This migration is ADDITIVE ONLY. projects.customer_id, projects.domain and
-- the customers table all stay, still populated, still read by everything that
-- reads them today. Nothing changes behaviour here — the read paths move over
-- in their own steps, and until then the two representations agree because the
-- backfill below seeds one from the other.

ALTER TABLE label_definitions ADD COLUMN system_key text;

-- One Customer and one Domain label per tenant, and no user label may claim a
-- key. Partial on live rows only, like the name index beside it.
CREATE UNIQUE INDEX idx_label_definitions_tenant_system_key
    ON label_definitions (tenant_id, system_key)
    WHERE system_key IS NOT NULL AND deleted_at IS NULL;

-- --- Seed the two built-in labels for every tenant ---------------------------

INSERT INTO label_definitions (tenant_id, name, system_key)
SELECT t.id, 'Customer', 'customer' FROM tenants t
ON CONFLICT DO NOTHING;

INSERT INTO label_definitions (tenant_id, name, system_key)
SELECT t.id, 'Domain', 'domain' FROM tenants t
ON CONFLICT DO NOTHING;

-- --- Customers become the Customer label's values ----------------------------
--
-- Soft-deleted customers come across as soft-deleted values, so a project that
-- still points at one keeps a row to reference and the audit trail survives.

INSERT INTO label_values (label_definition_id, value, created_at, updated_at, deleted_at)
SELECT d.id, c.name, c.created_at, c.updated_at, c.deleted_at
FROM customers c
JOIN label_definitions d ON d.tenant_id = c.tenant_id AND d.system_key = 'customer';

-- --- Distinct project domains become the Domain label's values ---------------
--
-- Domain is free text today, so the value set is whatever tenants have typed.
-- Trimmed, then folded case-insensitively because the value index is on
-- lower(value): "  Patents  " and "PATENTS" become one value. Which spelling
-- survives is decided by the lowest project id — deterministic, but arbitrary,
-- so a tenant may want to rename it afterwards.

INSERT INTO label_values (label_definition_id, value)
SELECT d.id, v.value
FROM (
    SELECT DISTINCT ON (p.tenant_id, lower(btrim(p.domain)))
           p.tenant_id, btrim(p.domain) AS value
    FROM projects p
    WHERE p.domain IS NOT NULL AND btrim(p.domain) <> ''
    ORDER BY p.tenant_id, lower(btrim(p.domain)), length(btrim(p.domain)), p.id
) v
JOIN label_definitions d ON d.tenant_id = v.tenant_id AND d.system_key = 'domain';

-- --- Existing projects get the matching picks --------------------------------

INSERT INTO project_labels (project_id, label_definition_id, label_value_id)
SELECT p.id, d.id, lv.id
FROM projects p
JOIN customers c ON c.id = p.customer_id
JOIN label_definitions d ON d.tenant_id = p.tenant_id AND d.system_key = 'customer'
JOIN label_values lv ON lv.label_definition_id = d.id AND lower(lv.value) = lower(c.name)
ON CONFLICT (project_id, label_definition_id) DO NOTHING;

INSERT INTO project_labels (project_id, label_definition_id, label_value_id)
SELECT p.id, d.id, lv.id
FROM projects p
JOIN label_definitions d ON d.tenant_id = p.tenant_id AND d.system_key = 'domain'
JOIN label_values lv ON lv.label_definition_id = d.id
                    AND lower(lv.value) = lower(btrim(p.domain))
WHERE p.domain IS NOT NULL AND btrim(p.domain) <> ''
ON CONFLICT (project_id, label_definition_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The picks and values seeded above go with the built-in labels; a user label
-- created since is untouched. project_labels rows cascade from label_values.
DELETE FROM label_values lv
USING label_definitions d
WHERE lv.label_definition_id = d.id AND d.system_key IS NOT NULL;

DELETE FROM label_definitions WHERE system_key IS NOT NULL;

DROP INDEX IF EXISTS idx_label_definitions_tenant_system_key;
ALTER TABLE label_definitions DROP COLUMN IF EXISTS system_key;

-- +goose StatementEnd
