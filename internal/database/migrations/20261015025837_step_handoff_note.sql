-- +goose Up
-- The comment written when the previous step was completed, for whoever works
-- this step. Set each time the step starts from a hand-off; NULL when the
-- hand-off came without a comment.
ALTER TABLE job_step_assignments ADD COLUMN handoff_note TEXT;

-- +goose Down
ALTER TABLE job_step_assignments DROP COLUMN handoff_note;
