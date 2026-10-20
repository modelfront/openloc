-- +goose Up
-- +goose StatementBegin

-- A template presets a project's labels, the same way it
-- presets languages and settings. Mirrors project_labels exactly: one value per
-- label, the definition stored alongside the value so the primary key can say
-- so, and a composite foreign key keeping the two columns honest.

CREATE TABLE template_labels (
    template_id         bigint NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    label_definition_id bigint NOT NULL REFERENCES label_definitions(id) ON DELETE CASCADE,
    label_value_id      bigint NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, label_definition_id),
    FOREIGN KEY (label_value_id, label_definition_id)
        REFERENCES label_values (id, label_definition_id) ON DELETE CASCADE
);

CREATE INDEX idx_template_labels_value ON template_labels (label_value_id);

-- Carry across what templates already hold in the legacy columns, matching the
-- backfill that moved projects. A template whose customer was soft-deleted
-- still resolves: the value came over carrying its deleted_at.
INSERT INTO template_labels (template_id, label_definition_id, label_value_id)
SELECT t.id, d.id, lv.id
FROM templates t
JOIN customers c ON c.id = t.customer_id
JOIN label_definitions d ON d.tenant_id = t.tenant_id AND d.system_key = 'customer'
JOIN label_values lv ON lv.label_definition_id = d.id AND lower(lv.value) = lower(c.name)
ON CONFLICT (template_id, label_definition_id) DO NOTHING;

-- A template's domain may be a string no project ever used, so the value set
-- gains it here rather than assuming the project backfill already created it.
INSERT INTO label_values (label_definition_id, value)
SELECT d.id, v.value
FROM (
    SELECT DISTINCT ON (t.tenant_id, lower(btrim(t.domain)))
           t.tenant_id, btrim(t.domain) AS value
    FROM templates t
    WHERE t.domain IS NOT NULL AND btrim(t.domain) <> ''
    ORDER BY t.tenant_id, lower(btrim(t.domain)), length(btrim(t.domain)), t.id
) v
JOIN label_definitions d ON d.tenant_id = v.tenant_id AND d.system_key = 'domain'
WHERE NOT EXISTS (
    SELECT 1 FROM label_values existing
    WHERE existing.label_definition_id = d.id AND lower(existing.value) = lower(v.value)
);

INSERT INTO template_labels (template_id, label_definition_id, label_value_id)
SELECT t.id, d.id, lv.id
FROM templates t
JOIN label_definitions d ON d.tenant_id = t.tenant_id AND d.system_key = 'domain'
JOIN label_values lv ON lv.label_definition_id = d.id
                    AND lower(lv.value) = lower(btrim(t.domain))
WHERE t.domain IS NOT NULL AND btrim(t.domain) <> ''
ON CONFLICT (template_id, label_definition_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS template_labels;

-- +goose StatementEnd
