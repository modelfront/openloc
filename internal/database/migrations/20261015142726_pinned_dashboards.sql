-- +goose Up
-- A person's Pinned view: dashboards of their own that hold references to
-- existing charts, never copies. Pinning a whole dashboard creates one that
-- follows it (new charts on the original appear here too); pinning a single
-- chart adds a reference to the pinned dashboard made from the chart's
-- dashboard.
CREATE TABLE pinned_dashboards (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id             bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                text   NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    -- The dashboard it was made from: where the next pin from that dashboard
    -- lands. Kept after a rename.
    source_dashboard_id bigint REFERENCES dashboards(id) ON DELETE SET NULL,
    -- While true, every live chart of the source shows here, pinned or not.
    follows_source      boolean NOT NULL DEFAULT false,
    -- Which controls the filter row shows, like dashboards.filters.
    filters             jsonb  NOT NULL DEFAULT '{"controls": []}',
    position            int    NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz
);

-- One pinned dashboard per source and person.
CREATE UNIQUE INDEX idx_pinned_dashboards_source ON pinned_dashboards (tenant_id, user_id, source_dashboard_id)
    WHERE deleted_at IS NULL AND source_dashboard_id IS NOT NULL;
CREATE INDEX idx_pinned_dashboards_user ON pinned_dashboards (tenant_id, user_id, position)
    WHERE deleted_at IS NULL;

-- A reference to a chart. One per person and chart: a chart is pinned or not.
-- Link rows, removed outright when unpinned.
CREATE TABLE pinned_charts (
    pinned_dashboard_id bigint NOT NULL REFERENCES pinned_dashboards(id) ON DELETE CASCADE,
    user_id             bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chart_id            bigint NOT NULL REFERENCES dashboard_charts(id) ON DELETE CASCADE,
    position            int    NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, chart_id)
);

CREATE INDEX idx_pinned_charts_dashboard ON pinned_charts (pinned_dashboard_id, position);

-- +goose Down
DROP TABLE pinned_charts;
DROP TABLE pinned_dashboards;
