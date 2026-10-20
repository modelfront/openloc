-- +goose Up
-- Dashboards: org-wide, multiple named dashboards whose
-- charts are created from the built-in chat (create_chart MCP tool). A chart
-- stores its QUERY DEFINITION (measure/dimensions/filters/relative range),
-- never data — the page re-runs the definition through the semantic layer on
-- load, so charts stay live without the model in the loop.

CREATE TABLE dashboards (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text   NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Unique per tenant because create_chart resolves dashboards by NAME
    -- (find-or-create): the model says "the QA dashboard" and must land on
    -- exactly one row. Deliberate contrast with ai_systems,
    -- where names are decorative labels.
    UNIQUE (tenant_id, name)
);

CREATE TABLE dashboard_charts (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    dashboard_id bigint NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    title        text   NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    -- {measure, dimensions[], filters{}, last_days} — validated by the
    -- metrics compiler at creation AND at every render (fail closed twice).
    definition   jsonb  NOT NULL,
    -- {type, x_key, series[]} — how to draw the rows.
    viz          jsonb  NOT NULL,
    position     int    NOT NULL DEFAULT 0,
    created_by   bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_dashboard_charts_dashboard ON dashboard_charts (dashboard_id, position, id);

-- +goose Down
DROP TABLE dashboard_charts;
DROP TABLE dashboards;
