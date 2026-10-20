-- +goose Up
-- +goose StatementBegin

-- Blueprint edits push onto the jobs sitting on one step
-- (RefreshJobStepAssignmentDeadlines, ReassignInFlightStepAssignments), which
-- filter by workflow_step_id and status. The UNIQUE (job_id, workflow_step_id)
-- index leads with job_id, so it cannot serve that lookup.
CREATE INDEX IF NOT EXISTS idx_job_step_assignments_step_status
    ON job_step_assignments (workflow_step_id, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_job_step_assignments_step_status;

-- +goose StatementEnd
