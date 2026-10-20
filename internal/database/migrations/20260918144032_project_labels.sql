-- +goose Up
-- +goose StatementBegin

-- User-defined project labels: a tenant names a label ("Country"), gives it a
-- set of allowed values ("Germany", "Japan"), and each project picks at most
-- one value per label.
--
-- Deliberately alongside the Customer FK and the free-text projects.domain, not
-- a replacement for them. Those two carry behaviour a generic label does not:
-- the customer name and the domain go to ModelFront /predict as
-- additional_metadata, customer is a metrics group-by dimension, and domain
-- ranks glossary concepts in the CAT term sidebar and in QA. A label here is
-- classification only.
--
-- Soft delete on both definitions and values: a project may
-- reference a value the tenant later retires, and the project's history should
-- still read back. project_labels is a link table, so it stays a hard delete.

CREATE TABLE label_definitions (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- Case-insensitive uniqueness per tenant, and only among live rows — so a
-- retired "Country" does not block a new one. Same shape as customers.
CREATE UNIQUE INDEX idx_label_definitions_tenant_name
    ON label_definitions (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE INDEX idx_label_definitions_tenant
    ON label_definitions (tenant_id) WHERE deleted_at IS NULL;

CREATE TABLE label_values (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    label_definition_id bigint NOT NULL REFERENCES label_definitions(id) ON DELETE CASCADE,
    value               text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz
);

CREATE UNIQUE INDEX idx_label_values_definition_value
    ON label_values (label_definition_id, lower(value)) WHERE deleted_at IS NULL;

CREATE INDEX idx_label_values_definition
    ON label_values (label_definition_id) WHERE deleted_at IS NULL;

-- Target for the composite foreign key below. Redundant with the primary key
-- on its own, but a composite FK needs a unique index over exactly its columns.
CREATE UNIQUE INDEX idx_label_values_id_definition
    ON label_values (id, label_definition_id);

-- One value per label per project: that is what the primary key says, and it is
-- why label_definition_id is stored here even though label_values already knows
-- it. The composite foreign key then makes the two columns agree — a row cannot
-- claim the "Country" label while pointing at a value of "Industry".
CREATE TABLE project_labels (
    project_id          bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    label_definition_id bigint NOT NULL REFERENCES label_definitions(id) ON DELETE CASCADE,
    label_value_id      bigint NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, label_definition_id),
    FOREIGN KEY (label_value_id, label_definition_id)
        REFERENCES label_values (id, label_definition_id) ON DELETE CASCADE
);

CREATE INDEX idx_project_labels_value ON project_labels (label_value_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS project_labels;
DROP INDEX IF EXISTS idx_label_values_id_definition;
DROP INDEX IF EXISTS idx_label_values_definition;
DROP INDEX IF EXISTS idx_label_values_definition_value;
DROP TABLE IF EXISTS label_values;
DROP INDEX IF EXISTS idx_label_definitions_tenant;
DROP INDEX IF EXISTS idx_label_definitions_tenant_name;
DROP TABLE IF EXISTS label_definitions;

-- +goose StatementEnd
