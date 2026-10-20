-- +goose Up
-- +goose StatementBegin

-- The Tasks list joins every in-progress assignment back to its job through
-- jobs.current_step_id. Without an index that join scans all of jobs.
CREATE INDEX IF NOT EXISTS idx_jobs_current_step_id
    ON jobs (current_step_id) WHERE current_step_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_jobs_current_step_id;
-- +goose StatementEnd
