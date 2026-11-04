-- ============================================================
-- Filter profiles
-- ============================================================

-- Reads go through filter_profile_catalog: the built-ins (tenant_id NULL,
-- read by every org) and the org's own profiles, so reads take
-- tenant_id = $tenant OR tenant_id IS NULL. Writes touch the org's table
-- alone, scoped by tenant_id. Org profiles are soft-deleted: live reads
-- filter deleted_at IS NULL.

-- name: ListFilterProfiles :many
-- The built-ins, then the org's own profiles; @format, when set, keeps one
-- format's.
SELECT * FROM filter_profile_catalog
WHERE (tenant_id = @tenant_id OR tenant_id IS NULL) AND deleted_at IS NULL
  AND (@format::text = '' OR format = @format::text)
ORDER BY format, tenant_id IS NOT NULL, lower(name), id;

-- name: ListFilterProfilesPage :many
-- One page by id after @cursor, with ListFilterProfiles' format filter.
SELECT * FROM filter_profile_catalog
WHERE (tenant_id = @tenant_id OR tenant_id IS NULL) AND deleted_at IS NULL AND id > @cursor
  AND (@format::text = '' OR format = @format::text)
ORDER BY id
LIMIT @lim;

-- name: GetFilterProfile :one
-- A live org profile of the tenant, or a built-in.
SELECT * FROM filter_profile_catalog
WHERE id = $1 AND (tenant_id = $2 OR tenant_id IS NULL) AND deleted_at IS NULL;

-- name: GetFilterProfileForImport :one
-- The profile a project or template names, deleted or not: an upload that
-- read its project's settings before a delete still resolves the profile.
SELECT * FROM filter_profile_catalog
WHERE id = $1 AND (tenant_id = $2 OR tenant_id IS NULL);

-- name: GetDefaultFilterProfile :one
-- A format's Default built-in: the newest version.
SELECT * FROM filter_profile_catalog
WHERE tenant_id IS NULL AND format = @format::text AND builtin_key LIKE 'builtin:' || @format::text || '-default@%'
ORDER BY id DESC
LIMIT 1;

-- name: CreateFilterProfile :one
INSERT INTO filter_profiles (tenant_id, name, format, options, based_on, created_by)
VALUES (@tenant_id, @name, @format, @options, sqlc.narg('based_on'), sqlc.narg('created_by'))
RETURNING id;

-- name: UpdateFilterProfile :one
-- Name and options, only while updated_at still equals
-- @expected_updated_at: a mismatch (someone saved in between) returns no
-- row. The format never changes.
UPDATE filter_profiles
SET name = @name, options = @options, updated_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND updated_at = @expected_updated_at
RETURNING id;

-- name: SoftDeleteFilterProfile :execrows
UPDATE filter_profiles
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CountFilterProfileUses :one
-- How many live projects and templates of the tenant name a profile.
SELECT
    (SELECT count(*) FROM projects p
     JOIN settings_profile_filter_profiles l ON l.settings_profile_id = p.settings_profile_id
     WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL AND l.filter_profile_id = @id)::bigint AS projects,
    (SELECT count(*) FROM templates t
     JOIN settings_profile_filter_profiles l ON l.settings_profile_id = t.settings_profile_id
     WHERE t.tenant_id = @tenant_id AND l.filter_profile_id = @id)::bigint AS templates;

-- name: ClearFilterProfileFromProjects :execrows
-- Takes a deleted profile off the tenant's projects: their uploads go back
-- to the format's Default.
DELETE FROM settings_profile_filter_profiles l
USING projects p
WHERE l.filter_profile_id = @id AND p.settings_profile_id = l.settings_profile_id
  AND p.tenant_id = @tenant_id;

-- ============================================================
-- A project's or template's choice per format
-- ============================================================

-- name: ListSettingsFilterProfiles :many
-- Internal: callers pass the settings profile id of a project or template
-- they read with its tenant. The chosen profile per format, with its name.
SELECT l.format, l.filter_profile_id, c.name
FROM settings_profile_filter_profiles l
JOIN filter_profile_catalog c ON c.id = l.filter_profile_id
WHERE l.settings_profile_id = $1
ORDER BY l.format;

-- name: GetSettingsFilterProfile :one
-- Internal: as ListSettingsFilterProfiles, for one format.
SELECT filter_profile_id FROM settings_profile_filter_profiles
WHERE settings_profile_id = $1 AND format = $2;

-- name: SetSettingsFilterProfile :execrows
-- Internal: as ListSettingsFilterProfiles. Stores only a built-in or a live
-- org profile of the format, holding a share lock on the profile row until
-- the caller commits, so a concurrent delete either waits or runs first and
-- this write stores nothing (0 rows).
INSERT INTO settings_profile_filter_profiles (settings_profile_id, format, filter_profile_id)
SELECT @settings_profile_id, @format::text, @filter_profile_id::bigint
WHERE EXISTS (SELECT 1 FROM filter_builtin_profiles b WHERE b.id = @filter_profile_id::bigint AND b.format = @format::text)
   OR EXISTS (SELECT 1 FROM filter_profiles o
              WHERE o.id = @filter_profile_id::bigint AND o.format = @format::text AND o.deleted_at IS NULL FOR SHARE)
ON CONFLICT (settings_profile_id, format) DO UPDATE SET filter_profile_id = EXCLUDED.filter_profile_id;

-- name: ClearSettingsFilterProfile :exec
-- Internal: as ListSettingsFilterProfiles. Back to the format's Default.
DELETE FROM settings_profile_filter_profiles WHERE settings_profile_id = $1 AND format = $2;

-- name: CopySettingsFilterProfiles :exec
-- Internal: copies a template's choices onto a new project's settings row.
INSERT INTO settings_profile_filter_profiles (settings_profile_id, format, filter_profile_id)
SELECT @dst::bigint, format, filter_profile_id FROM settings_profile_filter_profiles WHERE settings_profile_id = @src::bigint
ON CONFLICT (settings_profile_id, format) DO UPDATE SET filter_profile_id = EXCLUDED.filter_profile_id;

-- name: GetBuiltinFilterProfileByKey :one
SELECT * FROM filter_profile_catalog WHERE tenant_id IS NULL AND builtin_key = $1;

-- ============================================================
-- The org's default per format
-- ============================================================

-- name: ListFilterProfileDefaults :many
-- The org's default per format, with the profile's name.
SELECT d.format, d.filter_profile_id, c.name
FROM filter_profile_defaults d
JOIN filter_profile_catalog c ON c.id = d.filter_profile_id
WHERE d.tenant_id = $1
ORDER BY d.format;

-- name: SetFilterProfileDefault :execrows
-- Stores only a built-in or a live profile of the tenant of the format,
-- holding a share lock on the profile row until the caller commits, so a
-- concurrent delete either waits or runs first and this write stores nothing
-- (0 rows).
INSERT INTO filter_profile_defaults (tenant_id, format, filter_profile_id, updated_by)
SELECT @tenant_id, @format::text, @filter_profile_id::bigint, sqlc.narg('updated_by')
WHERE EXISTS (SELECT 1 FROM filter_builtin_profiles b WHERE b.id = @filter_profile_id::bigint AND b.format = @format::text)
   OR EXISTS (SELECT 1 FROM filter_profiles o
              WHERE o.id = @filter_profile_id::bigint AND o.tenant_id = @tenant_id AND o.format = @format::text
                AND o.deleted_at IS NULL FOR SHARE)
ON CONFLICT (tenant_id, format) DO UPDATE
SET filter_profile_id = EXCLUDED.filter_profile_id, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: ClearFilterProfileDefault :exec
-- Back to the format's built-in Default.
DELETE FROM filter_profile_defaults WHERE tenant_id = $1 AND format = $2;

-- name: IsFilterProfileDefault :one
-- Whether a profile is the tenant's default for its format.
SELECT EXISTS (SELECT 1 FROM filter_profile_defaults WHERE tenant_id = $1 AND filter_profile_id = $2);
