-- +goose Up
-- Segmentation rule sets become segmentation profiles: a project and a
-- template each name the profile their uploads are split with, so the
-- per-language Default goes. Every project and template gets the profile
-- its uploads used until now.
ALTER SEQUENCE segmentation_rule_set_id_seq RENAME TO segmentation_profile_id_seq;
ALTER TABLE segmentation_builtin_rule_sets RENAME TO segmentation_builtin_profiles;
ALTER INDEX segmentation_builtin_rule_sets_pkey RENAME TO segmentation_builtin_profiles_pkey;
ALTER INDEX segmentation_builtin_rule_sets_builtin_key_key RENAME TO segmentation_builtin_profiles_builtin_key_key;
ALTER TABLE segmentation_rule_sets RENAME TO segmentation_profiles;
ALTER INDEX segmentation_rule_sets_pkey RENAME TO segmentation_profiles_pkey;
ALTER INDEX segmentation_rule_sets_name_idx RENAME TO segmentation_profiles_name_idx;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_rule_sets_tenant_id_fkey TO segmentation_profiles_tenant_id_fkey;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_rule_sets_created_by_fkey TO segmentation_profiles_created_by_fkey;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_rule_sets_name_check TO segmentation_profiles_name_check;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_rule_sets_source_language_check TO segmentation_profiles_source_language_check;

-- The profile a project's or template's uploads are split with: a built-in
-- or one of the org's (both share one id sequence, so no foreign key). A
-- deleted org profile stays readable, so a project that names one keeps it.
ALTER TABLE settings_profiles ADD COLUMN segmentation_profile_id bigint;

-- What uploads used until now: the org's Default for the language, then for
-- its base language, then the newest built-in for the language, then for
-- its base language, then the newest any-language built-in.
UPDATE settings_profiles sp
SET segmentation_profile_id = pick.id
FROM (
    SELECT settings_profile_id, tenant_id, source_language AS lang,
           split_part(source_language, '-', 1) AS base
    FROM projects
    UNION ALL
    SELECT settings_profile_id, tenant_id, coalesce(source_language, ''),
           split_part(coalesce(source_language, ''), '-', 1)
    FROM templates
) src
CROSS JOIN LATERAL (
    SELECT coalesce(
        (SELECT o.id FROM segmentation_profiles o
         WHERE o.tenant_id = src.tenant_id AND o.is_default AND o.deleted_at IS NULL
           AND src.lang <> '' AND o.source_language = src.lang),
        (SELECT o.id FROM segmentation_profiles o
         WHERE o.tenant_id = src.tenant_id AND o.is_default AND o.deleted_at IS NULL
           AND src.base <> '' AND o.source_language = src.base),
        (SELECT b.id FROM segmentation_builtin_profiles b
         WHERE src.lang <> '' AND b.source_language = src.lang ORDER BY b.id DESC LIMIT 1),
        (SELECT b.id FROM segmentation_builtin_profiles b
         WHERE src.base <> '' AND b.source_language = src.base ORDER BY b.id DESC LIMIT 1),
        (SELECT b.id FROM segmentation_builtin_profiles b
         WHERE b.builtin_key LIKE 'builtin:default@%' ORDER BY b.id DESC LIMIT 1)
    ) AS id
) pick
WHERE sp.id = src.settings_profile_id;

DROP VIEW segmentation_rule_set_catalog;
DROP INDEX segmentation_rule_sets_default_idx;
ALTER TABLE segmentation_profiles DROP COLUMN is_default;

-- Both kinds in one shape for reads; tenant_id is NULL on a built-in.
CREATE VIEW segmentation_profile_catalog AS
SELECT id, NULL::bigint AS tenant_id, builtin_key, name, source_language, description,
       definition, NULL::bigint AS based_on,
       created_at, created_at AS updated_at, NULL::timestamptz AS deleted_at
FROM segmentation_builtin_profiles
UNION ALL
SELECT id, tenant_id, NULL::text, name, source_language, ''::text,
       definition, based_on,
       created_at, updated_at, deleted_at
FROM segmentation_profiles;

-- +goose Down
DROP VIEW segmentation_profile_catalog;
ALTER TABLE segmentation_profiles ADD COLUMN is_default boolean NOT NULL DEFAULT false;
-- A project's or template's profile becomes its language's Default where
-- that language has none yet, the newest org profile winning.
UPDATE segmentation_profiles o
SET is_default = true
WHERE o.id IN (
    SELECT DISTINCT ON (p.tenant_id, p.source_language) p.id
    FROM settings_profiles sp
    JOIN segmentation_profiles p ON p.id = sp.segmentation_profile_id
    WHERE p.deleted_at IS NULL
    ORDER BY p.tenant_id, p.source_language, p.id DESC
);
ALTER TABLE settings_profiles DROP COLUMN segmentation_profile_id;

ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_profiles_source_language_check TO segmentation_rule_sets_source_language_check;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_profiles_name_check TO segmentation_rule_sets_name_check;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_profiles_created_by_fkey TO segmentation_rule_sets_created_by_fkey;
ALTER TABLE segmentation_profiles RENAME CONSTRAINT segmentation_profiles_tenant_id_fkey TO segmentation_rule_sets_tenant_id_fkey;
ALTER INDEX segmentation_profiles_name_idx RENAME TO segmentation_rule_sets_name_idx;
ALTER INDEX segmentation_profiles_pkey RENAME TO segmentation_rule_sets_pkey;
ALTER TABLE segmentation_profiles RENAME TO segmentation_rule_sets;
ALTER INDEX segmentation_builtin_profiles_builtin_key_key RENAME TO segmentation_builtin_rule_sets_builtin_key_key;
ALTER INDEX segmentation_builtin_profiles_pkey RENAME TO segmentation_builtin_rule_sets_pkey;
ALTER TABLE segmentation_builtin_profiles RENAME TO segmentation_builtin_rule_sets;
ALTER SEQUENCE segmentation_profile_id_seq RENAME TO segmentation_rule_set_id_seq;

CREATE UNIQUE INDEX segmentation_rule_sets_default_idx
    ON segmentation_rule_sets (tenant_id, source_language)
    WHERE is_default AND deleted_at IS NULL;

CREATE VIEW segmentation_rule_set_catalog AS
SELECT id, NULL::bigint AS tenant_id, builtin_key, name, source_language, description,
       false AS is_default, definition, NULL::bigint AS based_on,
       created_at, created_at AS updated_at, NULL::timestamptz AS deleted_at
FROM segmentation_builtin_rule_sets
UNION ALL
SELECT id, tenant_id, NULL::text, name, source_language, ''::text,
       is_default, definition, based_on,
       created_at, updated_at, deleted_at
FROM segmentation_rule_sets;
