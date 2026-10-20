-- +goose Up
-- +goose StatementBegin

-- workflow_definition_steps.step_definition_id was ON DELETE RESTRICT, which
-- blocks hard-deleting a tenant that owns any library workflow: the tenant
-- cascade drops step_definitions while the workflow's composition rows still
-- reference them, and RESTRICT aborts the whole delete (500). Every org now
-- starts with a default workflow, so this hit every tenant delete.
--
-- Step definitions are only ever SOFT-deleted by the app (the in-use guard
-- lives in stepdef.HandleDelete, backed by a row lock), so an individual step
-- definition is never hard-deleted — the only hard delete is this tenant
-- cascade, where cascading the composition rows is the correct behaviour.
ALTER TABLE workflow_definition_steps
    DROP CONSTRAINT workflow_definition_steps_step_definition_id_fkey,
    ADD CONSTRAINT workflow_definition_steps_step_definition_id_fkey
        FOREIGN KEY (step_definition_id) REFERENCES step_definitions(id) ON DELETE CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE workflow_definition_steps
    DROP CONSTRAINT workflow_definition_steps_step_definition_id_fkey,
    ADD CONSTRAINT workflow_definition_steps_step_definition_id_fkey
        FOREIGN KEY (step_definition_id) REFERENCES step_definitions(id) ON DELETE RESTRICT;

-- +goose StatementEnd
