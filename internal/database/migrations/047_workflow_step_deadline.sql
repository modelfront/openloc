-- +goose Up
-- Workflow steps (the per-settings-profile blueprint) carry an
-- optional default deadline, set at project creation. On file upload each
-- job_step_assignment inherits its step's deadline (overridable in the upload
-- dialog). Absolute timestamp — the upload dialog is where stale blueprint
-- dates get adjusted per upload.
ALTER TABLE workflow_steps ADD COLUMN deadline timestamptz;

-- +goose Down
ALTER TABLE workflow_steps DROP COLUMN deadline;
