-- ============================================================
-- Segmentation profiles
-- ============================================================

-- Reads go through segmentation_profile_catalog: the built-ins (tenant_id
-- NULL, read by every org) and the org's own profiles, so reads take
-- tenant_id = $tenant OR tenant_id IS NULL. Writes touch the org's table
-- alone, scoped by tenant_id. Org profiles are soft-deleted: live reads
-- filter deleted_at IS NULL.

-- name: ListSegmentationProfiles :many
-- The built-ins, then the org's own profiles.
SELECT * FROM segmentation_profile_catalog
WHERE (tenant_id = $1 OR tenant_id IS NULL) AND deleted_at IS NULL
ORDER BY tenant_id IS NOT NULL, source_languages, lower(name), id;

-- name: GetSegmentationProfile :one
-- An org profile of the tenant, or a built-in.
SELECT * FROM segmentation_profile_catalog
WHERE id = $1 AND (tenant_id = $2 OR tenant_id IS NULL) AND deleted_at IS NULL;

-- name: GetSegmentationProfileForImport :one
-- The profile a project or template names, deleted or not: an upload that
-- read its project's settings before a delete still resolves the profile.
SELECT * FROM segmentation_profile_catalog
WHERE id = $1 AND (tenant_id = $2 OR tenant_id IS NULL);

-- name: ListBuiltinSegmentationProfiles :many
-- The built-ins, newest first.
SELECT * FROM segmentation_profile_catalog
WHERE tenant_id IS NULL
ORDER BY id DESC;

-- name: CreateSegmentationProfile :one
INSERT INTO segmentation_profiles (tenant_id, name, source_languages, definition, based_on, created_by)
VALUES (@tenant_id, @name, @source_languages::text[], @definition, sqlc.narg('based_on'), sqlc.narg('created_by'))
RETURNING id;

-- name: UpdateSegmentationProfile :one
-- Full replace, only while updated_at still equals @expected_updated_at: a
-- mismatch (someone saved in between) returns no row.
UPDATE segmentation_profiles
SET name            = @name,
    source_languages = @source_languages::text[],
    definition      = @definition,
    updated_at      = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND updated_at = @expected_updated_at
RETURNING id;

-- name: SoftDeleteSegmentationProfile :execrows
UPDATE segmentation_profiles
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: ListSegmentationProfilesPage :many
-- One page by id: the built-ins and the org's profiles after @cursor; when
-- @source_language is set, those that suit it (the language or its base
-- language is on the profile's list, or the list is empty: any language).
SELECT * FROM segmentation_profile_catalog
WHERE (tenant_id = @tenant_id OR tenant_id IS NULL) AND deleted_at IS NULL AND id > @cursor
  AND (@source_language::text = '' OR cardinality(source_languages) = 0
       OR source_languages && ARRAY[@source_language::text, @base_language::text])
ORDER BY id
LIMIT @lim;

-- name: CountSegmentationProfileUses :one
-- How many live projects and templates of the tenant name a profile.
SELECT
    (SELECT count(*) FROM projects p JOIN settings_profiles sp ON sp.id = p.settings_profile_id
     WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL AND sp.segmentation_profile_id = @id)::bigint AS projects,
    (SELECT count(*) FROM templates t JOIN settings_profiles sp ON sp.id = t.settings_profile_id
     WHERE t.tenant_id = @tenant_id AND sp.segmentation_profile_id = @id)::bigint AS templates;

-- name: ListSegmentationProfileTemplateLanguages :many
-- The source languages ('' for none) of the tenant's templates that name a
-- profile.
SELECT coalesce(t.source_language, '')::text AS source_language
FROM templates t JOIN settings_profiles sp ON sp.id = t.settings_profile_id
WHERE t.tenant_id = @tenant_id AND sp.segmentation_profile_id = @id;


-- name: ClearSegmentationProfileFromProjects :execrows
-- Takes a deleted profile off the tenant's projects: their next upload asks
-- for another profile.
UPDATE settings_profiles sp SET segmentation_profile_id = NULL
WHERE sp.tenant_id = @tenant_id AND sp.segmentation_profile_id = @id::bigint
  AND EXISTS (SELECT 1 FROM projects p WHERE p.settings_profile_id = sp.id AND p.tenant_id = @tenant_id);
