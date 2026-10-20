-- +goose Up
-- +goose StatementBegin

-- Drop 'in_review' from project_status — review is a workflow step, not a
-- project state.
--
-- Postgres cannot remove a value from an enum, so the type is swapped and
-- existing 'in_review' rows land on 'in_progress'. ALTER COLUMN TYPE rewrites
-- the table and rebuilds idx_projects_tenant_status.

ALTER TYPE project_status RENAME TO project_status_old;

CREATE TYPE project_status AS ENUM (
    'draft',
    'in_progress',
    'completed',
    'on_hold'
);

ALTER TABLE projects ALTER COLUMN status DROP DEFAULT;

ALTER TABLE projects
    ALTER COLUMN status TYPE project_status
    USING (CASE WHEN status::text = 'in_review' THEN 'in_progress'
                ELSE status::text END)::project_status;

ALTER TABLE projects ALTER COLUMN status SET DEFAULT 'in_progress';

DROP TYPE project_status_old;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Lossy: restores the five-value type, but the rows that were 'in_review'
-- before the Up became 'in_progress' and are indistinguishable from rows that
-- were already 'in_progress'. Nothing is moved back.

ALTER TYPE project_status RENAME TO project_status_old;

CREATE TYPE project_status AS ENUM (
    'draft',
    'in_progress',
    'in_review',
    'completed',
    'on_hold'
);

ALTER TABLE projects ALTER COLUMN status DROP DEFAULT;

ALTER TABLE projects
    ALTER COLUMN status TYPE project_status
    USING status::text::project_status;

ALTER TABLE projects ALTER COLUMN status SET DEFAULT 'in_progress';

DROP TYPE project_status_old;

-- +goose StatementEnd
