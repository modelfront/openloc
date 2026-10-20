-- TM profiles: a tenant-wide, named bundle of TMs + match scoring that
-- projects and templates link to live. The TM rows and the scoring scalars
-- live on the profile's own settings_profiles row (tm_profiles.settings_profile_id);
-- these queries cover the profile row itself.
-- All reads filter tenant_id AND deleted_at IS NULL.

-- name: ListTMProfiles :many
-- One row per live profile with everything the list page renders: scoring from
-- the owned settings row, how many TMs it bundles, how many project/template
-- settings rows link to it, and the creator's display name (same precedence as
-- ListTMCollections: users.name, then email, then blank).
SELECT tp.id, tp.tenant_id, tp.settings_profile_id, tp.name, tp.description,
       tp.created_by, tp.created_at, tp.updated_at,
       sp.fuzzy_threshold, sp.sublanguage_penalty_pct, sp.near_exact_penalty_pct, sp.tm_lookup_order,
       (SELECT count(*) FROM settings_tm_assignments sta WHERE sta.settings_profile_id = tp.settings_profile_id)::int AS tm_count,
       (SELECT count(*) FROM settings_profiles l WHERE l.tm_profile_id = tp.id
          AND settings_profile_live(l.id))::int AS usage_count,
       COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM tm_profiles tp
JOIN settings_profiles sp ON sp.id = tp.settings_profile_id
LEFT JOIN users u ON u.id = tp.created_by
WHERE tp.tenant_id = $1 AND tp.deleted_at IS NULL
ORDER BY lower(tp.name);

-- name: GetTMProfile :one
SELECT * FROM tm_profiles
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CreateTMProfile :one
INSERT INTO tm_profiles (tenant_id, settings_profile_id, name, description, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateTMProfile :one
UPDATE tm_profiles
SET name = $3, description = $4, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTMProfile :exec
UPDATE tm_profiles
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL;

-- name: CountTMProfileUsage :one
-- How many projects and templates follow this profile. The delete handler
-- refuses (409) while either count is non-zero; the FK is ON DELETE RESTRICT
-- as a backstop. Tenant-scoped through settings_profiles.tenant_id.
SELECT count(p.id)::int AS project_count,
       count(t.id)::int AS template_count
FROM settings_profiles sp
LEFT JOIN projects p ON p.settings_profile_id = sp.id AND p.deleted_at IS NULL
LEFT JOIN templates t ON t.settings_profile_id = sp.id
WHERE sp.tm_profile_id = $1 AND sp.tenant_id = $2;

-- name: CountTMProfileFollowers :one
-- The edit guard's count: who would be surprised if this profile changed.
-- A draft project is the New Project form saved as a row,
-- so a profile created from that form is followed by the form itself — nobody
-- to surprise, and counting it refuses the PM's next edit citing a project
-- they cannot see. Deletion still uses CountTMProfileUsage, which counts
-- drafts: the FK is ON DELETE RESTRICT, so a draft must keep blocking it or a
-- clean 409 becomes a 500.
SELECT count(p.id)::int AS project_count,
       count(t.id)::int AS template_count
FROM settings_profiles sp
LEFT JOIN projects p ON p.settings_profile_id = sp.id AND p.status <> 'draft' AND p.deleted_at IS NULL
LEFT JOIN templates t ON t.settings_profile_id = sp.id
WHERE sp.tm_profile_id = $1 AND sp.tenant_id = $2;

-- name: GetTMProfileWithStats :one
-- Detail-page shape: the same columns as ListTMProfiles for one profile.
SELECT tp.id, tp.tenant_id, tp.settings_profile_id, tp.name, tp.description,
       tp.created_by, tp.created_at, tp.updated_at,
       sp.fuzzy_threshold, sp.sublanguage_penalty_pct, sp.near_exact_penalty_pct, sp.tm_lookup_order,
       (SELECT count(*) FROM settings_tm_assignments sta WHERE sta.settings_profile_id = tp.settings_profile_id)::int AS tm_count,
       (SELECT count(*) FROM settings_profiles l WHERE l.tm_profile_id = tp.id
          AND settings_profile_live(l.id))::int AS usage_count,
       COALESCE(NULLIF(u.name, ''), u.email, '')::text AS created_by_name
FROM tm_profiles tp
JOIN settings_profiles sp ON sp.id = tp.settings_profile_id
LEFT JOIN users u ON u.id = tp.created_by
WHERE tp.id = $1 AND tp.tenant_id = $2 AND tp.deleted_at IS NULL;
