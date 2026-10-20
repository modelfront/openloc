-- +goose Up
-- +goose StatementBegin

-- Per-target-language settings for a profile's workflow steps: the blueprint
-- twin of job_step_assignments, keyed by target language where that one is
-- keyed by job. A project's jobs are file × target language, so one step can
-- differ per language — for example a deadline (a short language first, a
-- long one later).
--
-- Named for the row, not for the column: every field here overrides the
-- matching one on workflow_steps for a single language, and more will join
-- `deadline`. A row exists as soon as ANY of them is set, so each is nullable
-- and the step's own value covers a NULL:
--     deadline(step, language) = this table's deadline ?? workflow_steps.deadline
--                                ?? projects.deadline ?? none (never for an ai step)
--
-- Resolved when a job_step_assignment is written, so every reader of
-- jsa.deadline (the reminder cron, Tasks, the job list, MCP) is unaffected.
--
-- The natural key is the pair, which makes a duplicate language impossible.
-- No surrogate id: unlike job_step_assignments, which jobs.current_step_id
-- points at, nothing references a row here.
--
-- Tenant scoping runs through workflow_steps -> settings_profiles; a cascade
-- child, so the hard delete is the sanctioned carve-out.
CREATE TABLE workflow_step_assignments (
    workflow_step_id bigint      NOT NULL REFERENCES workflow_steps(id) ON DELETE CASCADE,
    target_language  text        NOT NULL,
    deadline         timestamptz,
    PRIMARY KEY (workflow_step_id, target_language)
);

-- An ai step completes itself; nobody waits on it, so it carries no deadline.
-- An ai step never had a reader for its deadline, so this drops a value
-- nothing consumed. The Down cannot restore it: the old values are not stashed.
UPDATE workflow_steps SET deadline = NULL WHERE step_type = 'ai';

ALTER TABLE workflow_steps
    ADD CONSTRAINT workflow_steps_ai_no_deadline CHECK (step_type <> 'ai' OR deadline IS NULL);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_ai_no_deadline;
DROP TABLE workflow_step_assignments;

-- +goose StatementEnd
