-- +goose Up
-- +goose StatementBegin

-- Project lifecycle status + creator + customer link.
--
-- Three columns added in one migration because they all surface in the same
-- UI (project list page filters) and share the same risk profile (additive,
-- NULLABLE / DEFAULT-backfilled). Sequenced AFTER 024_customers because
-- customer_id FK references that table.
--
-- Why ENUM for status (not CHECK constraint or FK lookup):
--   - Type-safe, sqlc generates a Go enum type.
--   - Values are universal TMS lifecycle states, not tenant-customizable.
--   - Trade-off: adding a value later requires ALTER TYPE (acquires
--     ACCESS EXCLUSIVE briefly). Accepted — this enum is structurally stable.
--
-- Why default 'in_progress' (not 'draft'):
--   Existing projects in production are already past the "drafting" phase.
--   Defaulting to 'in_progress' backfills correctly. New projects created
--   via /projects/new can override to 'draft' if the PM wants.
--
-- Why `created_by` NULLABLE:
--   No clean way to backfill historical projects with a creator id. Filters
--   that say "Created by me" exclude NULLs by default; a separate
--   "Show legacy projects" toggle in the frontend can opt-in to NULL rows.
--
-- Why `customer_id` NULLABLE + ON DELETE SET NULL:
--   Many projects are internal work and have no customer. Deleting a
--   customer (soft-delete) doesn't cascade-destroy projects. Hard delete
--   would set customer_id to NULL on the project, preserving history.
--
-- IMPORTANT: `projects.status` is project-LIFECYCLE state. It is distinct from
-- `workflow_step_progress.status` (per-step) and `translations.status`
-- (per-segment). Three "status" columns at three different scopes. Don't
-- conflate.

CREATE TYPE project_status AS ENUM (
    'draft',
    'in_progress',
    'in_review',
    'completed',
    'on_hold'
);

ALTER TABLE projects
    ADD COLUMN status        project_status NOT NULL DEFAULT 'in_progress',
    ADD COLUMN created_by    bigint REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN customer_id   bigint REFERENCES customers(id) ON DELETE SET NULL;

-- Indexes for the two hottest filter patterns. Filter on language uses the
-- existing schema's array columns (no new index needed for MVP — query
-- planner picks up tenant_id index and filters in memory).
CREATE INDEX idx_projects_tenant_status
    ON projects (tenant_id, status);

CREATE INDEX idx_projects_tenant_customer
    ON projects (tenant_id, customer_id) WHERE customer_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_projects_tenant_customer;
DROP INDEX IF EXISTS idx_projects_tenant_status;

ALTER TABLE projects
    DROP COLUMN IF EXISTS customer_id,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS status;

DROP TYPE IF EXISTS project_status;

-- +goose StatementEnd
