-- +goose Up
-- AI-only workflow steps. A workflow step can now reference an AI
-- engine and be marked AI-only. When ai_only is true the step is executed by
-- the referenced AI engine rather than a human, so role_required /
-- default_assignee become irrelevant. ON DELETE SET NULL: deleting an engine
-- leaves the step intact (the FE surfaces the dangling reference to re-pick),
-- mirroring the default-assignee deleted-user pattern.
ALTER TABLE workflow_steps
    ADD COLUMN ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL,
    ADD COLUMN ai_only      boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE workflow_steps
    DROP COLUMN ai_only,
    DROP COLUMN ai_engine_id;
