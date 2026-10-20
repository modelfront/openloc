-- +goose Up
-- What the earlier attempts of a retried pre-translation filled. asynq retries
-- a failed attempt, and the retry does only the remaining work, so the last
-- attempt adds this to its own outcome to report the whole run in History.
-- One row per job at most; the worker writes it on a failed attempt that will
-- be retried and deletes it when the run completes, is stopped, fails for
-- good, or a new run starts. The JSON names its run (the task's enqueue time),
-- so a row another run left is never read.
CREATE TABLE job_run_outcomes (
    job_id     bigint PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    outcome    jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE job_run_outcomes;
