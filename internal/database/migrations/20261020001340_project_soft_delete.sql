-- +goose Up
-- Deleting a project sets deleted_at: reads skip the row at once, and a worker
-- task purges the project and everything it owns in batches.
ALTER TABLE projects ADD COLUMN deleted_at timestamptz;
-- Also serves settings_profile_live.
CREATE INDEX idx_projects_deleted ON projects (settings_profile_id) WHERE deleted_at IS NOT NULL;

-- False for the settings profile of a deleted project still waiting for its
-- purge. The "used by" counts of shared resources skip such a profile.
-- +goose StatementBegin
CREATE FUNCTION settings_profile_live(profile_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT NOT EXISTS (
        SELECT 1 FROM projects
        WHERE settings_profile_id = profile_id AND deleted_at IS NOT NULL
    )
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS settings_profile_live(bigint);
DROP INDEX IF EXISTS idx_projects_deleted;
WITH gone AS (
    DELETE FROM projects WHERE deleted_at IS NOT NULL RETURNING settings_profile_id
)
DELETE FROM settings_profiles WHERE id IN (SELECT settings_profile_id FROM gone);
ALTER TABLE projects DROP COLUMN deleted_at;
