-- +goose Up
-- Move the deadline from the job to its workflow-step assignment. The job's
-- effective deadline is now the deadline of its ACTIVE step (the
-- job_step_assignments row referenced by jobs.current_step_id). Storing it
-- per-assignment lets each step carry its own deadline later. There is NO
-- project fallback — an assignment with no deadline simply has none.
--
-- jobs.deadline was never populated via the UI (only a hand-callable API), so
-- there is no data worth migrating; we drop it outright.
ALTER TABLE job_step_assignments ADD COLUMN deadline timestamptz;
CREATE INDEX idx_jsa_deadline ON job_step_assignments USING btree (deadline) WHERE (deadline IS NOT NULL);

DROP INDEX IF EXISTS idx_jobs_deadline;
ALTER TABLE jobs DROP COLUMN deadline;

-- +goose Down
ALTER TABLE jobs ADD COLUMN deadline timestamptz;
CREATE INDEX idx_jobs_deadline ON jobs USING btree (deadline) WHERE (deadline IS NOT NULL);
DROP INDEX IF EXISTS idx_jsa_deadline;
ALTER TABLE job_step_assignments DROP COLUMN deadline;
