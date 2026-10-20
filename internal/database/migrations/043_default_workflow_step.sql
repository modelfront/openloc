-- +goose Up
-- Workflow steps are now mandatory (every profile gets a
-- default Translation step). Backfill a
-- default "translation" step for every settings_profile that lacks one, then
-- create matching job_step_assignments for every job whose profile gets a
-- new step (since file upload skipped the assignment when wfSteps was empty).
-- After this, every job in the system has a workflow step assignment + a
-- non-NULL jobs.current_step_id.

-- Step 1: add a default "translation" step to profiles without one.
INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required)
SELECT sp.id, 'translation', 0, 'member'
FROM settings_profiles sp
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_steps ws WHERE ws.settings_profile_id = sp.id
);

-- Step 2: create job_step_assignments + set jobs.current_step_id for every
-- job that has NO existing assignment. Jobs whose workflow ran and completed
-- (current_step_id flipped back to NULL when the final step finished) still
-- have their old assignment rows — we leave those alone. The "no existing
-- assignment" filter is required to avoid hitting the
-- job_step_assignments_job_id_workflow_step_id_key unique constraint.
WITH first_step_per_profile AS (
    SELECT DISTINCT ON (settings_profile_id)
        id AS step_id, settings_profile_id
    FROM workflow_steps
    ORDER BY settings_profile_id, step_order ASC, id ASC
),
new_assignments AS (
    INSERT INTO job_step_assignments (job_id, workflow_step_id, status)
    SELECT j.id, fsp.step_id, 'in_progress'
    FROM jobs j
    JOIN files f ON f.id = j.file_id
    JOIN projects p ON p.id = f.project_id
    JOIN first_step_per_profile fsp ON fsp.settings_profile_id = p.settings_profile_id
    WHERE j.current_step_id IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM job_step_assignments jsa WHERE jsa.job_id = j.id
      )
    RETURNING id AS assignment_id, job_id
)
UPDATE jobs SET current_step_id = na.assignment_id
FROM new_assignments na
WHERE jobs.id = na.job_id;

-- +goose Down
-- Reversal is not safe in general — we can't reliably tell which steps and
-- assignments were created by this migration vs by users between up and down.
-- Removing them risks orphaning downstream work. Down is a no-op; rerunning
-- Up is idempotent (the NOT EXISTS guard prevents duplicate steps; the
-- IS NULL filter prevents duplicate assignments).
SELECT 1;
