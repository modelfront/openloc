-- +goose Up
-- +goose StatementBegin

-- A job's workflow is linear: at most one task is in progress at a time. The
-- task edit guard and the editor both read "the" in-progress task, and
-- reopen-by-edit refuses when one exists; this index makes a second one an
-- error instead of an ambiguous state.
CREATE UNIQUE INDEX IF NOT EXISTS uq_job_step_assignments_one_in_progress
    ON job_step_assignments (job_id)
    WHERE status = 'in_progress';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS uq_job_step_assignments_one_in_progress;

-- +goose StatementEnd
