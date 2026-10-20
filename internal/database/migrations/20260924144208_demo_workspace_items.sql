-- +goose Up
-- +goose StatementBegin

-- Manifest of what demodata.Create put into a tenant: one row per project,
-- translation memory, term base, QA profile, AI engine, AI prompt, MT router,
-- customer and label it created. Things it found and reused are not listed,
-- so the rows are exactly what a "remove demo data" action may take away.
--
-- A side table rather than a marker in each entity: the demo names are shown
-- in the help-page screenshots and must stay as they are, and TMs, term bases,
-- QA profiles and AI engines have no label or metadata column to carry a flag.
-- No foreign key on entity_id — it points into several tables — so readers
-- join to the entity table and treat a missing row as gone.
CREATE TABLE demo_workspace_items (
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    entity_type text NOT NULL CHECK (entity_type IN (
        'project', 'tm', 'term_base', 'qa_profile', 'ai_engine', 'ai_prompt',
        'mt_router', 'customer', 'label_definition', 'label_value'
    )),
    entity_id   bigint NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, entity_type, entity_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS demo_workspace_items;

-- +goose StatementEnd
