-- +goose Up
-- Project-level audit log. One row per user-visible
-- project mutation: creation, metadata/settings changes, assignment swaps,
-- file lifecycle, job/workflow actions. Segment-level edits stay in
-- translation_history — this table never duplicates them.
CREATE TABLE project_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    -- Denormalized for single-table tenant-scoped reads (mcp_tool_calls precedent).
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- Text + CHECK instead of an enum: new event types are a Go constant,
    -- not a migration. Known values live in internal/projectevents/events.go.
    event_type  text NOT NULL CHECK (event_type <> ''),
    -- NULL = system action (no user attribution).
    actor_id    bigint REFERENCES users(id) ON DELETE SET NULL,
    -- Correlation ids, deliberately NOT FKs: files/jobs get deleted and the
    -- event must survive with its id intact. Human-readable snapshots
    -- (file_name, target_language, actor_name, ...) always live in details.
    file_id     bigint,
    job_id      bigint,
    -- Field diffs use {"changes": {"<field>": {"from": x, "to": y}}}.
    details     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Dominant read path: "history of this project, newest first". id is an
-- identity column, so id order == insert order and id is the stable cursor.
CREATE INDEX idx_project_events_project ON project_events (project_id, id DESC);

-- +goose Down
DROP TABLE IF EXISTS project_events;
