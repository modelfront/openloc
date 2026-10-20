-- +goose Up
-- +goose StatementBegin

-- TM profiles: a named, tenant-wide bundle of translation memories (with
-- per-TM mode/priority/penalty) plus the match-scoring scalars. A project or
-- template LINKS to one; the link is live, so editing the profile changes every
-- linked project at once. Detaching copies the profile's rows + scoring into
-- the project's own settings and clears the link.
--
-- A profile owns a settings_profiles row of its own: settings_tm_assignments,
-- the three scoring columns, and every lookup query already key on a
-- settings_profile_id, so the profile reuses all of them instead of growing a
-- parallel table. The row's other columns (mt_on_upload, qa_profile_id, …) are
-- unused for a profile and stay at their defaults.
CREATE TABLE tm_profiles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    settings_profile_id bigint NOT NULL UNIQUE REFERENCES settings_profiles(id),
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- Nullable + SET NULL, matching tm_collections.created_by: an asset must
    -- outlive the account that made it.
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Soft delete. Reads filter deleted_at IS NULL.
    deleted_at timestamptz
);

-- Name is unique per tenant, case-insensitive, among live profiles only.
CREATE UNIQUE INDEX uq_tm_profiles_tenant_name
    ON tm_profiles (tenant_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX idx_tm_profiles_tenant ON tm_profiles (tenant_id) WHERE deleted_at IS NULL;

-- The link. RESTRICT, not SET NULL: a project silently losing its TMs is the
-- failure mode to prevent. The API refuses to delete a referenced profile
-- (409) before this ever fires.
ALTER TABLE settings_profiles
    ADD COLUMN tm_profile_id bigint REFERENCES tm_profiles(id) ON DELETE RESTRICT;
CREATE INDEX idx_settings_profiles_tm_profile
    ON settings_profiles (tm_profile_id) WHERE tm_profile_id IS NOT NULL;

-- Resolves the settings_profiles row whose TM rows + scoring apply to a
-- project/template settings row: the linked TM profile's own row, else the row
-- itself. The lookup queries call this so no caller has to know about links.
CREATE FUNCTION tm_scope_profile_id(sp_id bigint) RETURNS bigint
LANGUAGE sql STABLE
AS $$
    SELECT COALESCE(tp.settings_profile_id, sp.id)
    FROM settings_profiles sp
    LEFT JOIN tm_profiles tp ON tp.id = sp.tm_profile_id AND tp.deleted_at IS NULL
    WHERE sp.id = sp_id
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS tm_scope_profile_id(bigint);
DROP INDEX IF EXISTS idx_settings_profiles_tm_profile;
ALTER TABLE settings_profiles DROP COLUMN IF EXISTS tm_profile_id;
DROP TABLE IF EXISTS tm_profiles;
-- +goose StatementEnd
