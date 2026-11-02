-- +goose Up
-- The last workflow step a job's translations may still be cleared in: the
-- first human or hybrid step of the project's workflow. Automated steps before
-- it (Pre-translation, AI) don't count. A workflow with no human or hybrid
-- step allows only its first step (order 0). Every clear check and the job's
-- `last_clear_step_order` read these functions; a project's job list calls the
-- project one once, since all its jobs share the project's workflow.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION project_last_clear_step_order(p_project_id bigint) RETURNS int
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT COALESCE(MIN(ws.step_order) FILTER (WHERE ws.step_type IN ('human', 'hybrid')), 0)::int
    FROM projects p
    JOIN workflow_steps ws ON ws.settings_profile_id = p.settings_profile_id
    WHERE p.id = p_project_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_last_clear_step_order(p_job_id bigint) RETURNS int
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT COALESCE((
        SELECT project_last_clear_step_order(f.project_id)
        FROM jobs j
        JOIN files f ON f.id = j.file_id
        WHERE j.id = p_job_id
    ), 0)
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS job_last_clear_step_order(bigint);
DROP FUNCTION IF EXISTS project_last_clear_step_order(bigint);
