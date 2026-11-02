-- +goose Up
-- An org segmentation profile suits a list of source languages; an empty list
-- suits any language. A built-in keeps one language ('' for any), and the
-- catalog shows both as a list.
DROP VIEW segmentation_profile_catalog;

ALTER TABLE segmentation_profiles ADD COLUMN source_languages text[] NOT NULL DEFAULT '{}';
UPDATE segmentation_profiles SET source_languages = ARRAY[source_language];
ALTER TABLE segmentation_profiles ALTER COLUMN source_languages DROP DEFAULT;
ALTER TABLE segmentation_profiles ADD CONSTRAINT segmentation_profiles_source_languages_check
    CHECK (array_position(source_languages, '') IS NULL);
ALTER TABLE segmentation_profiles DROP COLUMN source_language;

CREATE VIEW segmentation_profile_catalog AS
SELECT id, NULL::bigint AS tenant_id, builtin_key, name,
       (CASE WHEN source_language = '' THEN '{}' ELSE ARRAY[source_language] END)::text[] AS source_languages,
       description, definition, NULL::bigint AS based_on,
       created_at, created_at AS updated_at, NULL::timestamptz AS deleted_at
FROM segmentation_builtin_profiles
UNION ALL
SELECT id, tenant_id, NULL::text, name, source_languages, ''::text,
       definition, based_on,
       created_at, updated_at, deleted_at
FROM segmentation_profiles;

-- +goose Down
DROP VIEW segmentation_profile_catalog;

-- A profile keeps its first language. One for any language has none to keep
-- and gets 'und' (undetermined), which suits no project.
ALTER TABLE segmentation_profiles ADD COLUMN source_language text NOT NULL DEFAULT '';
UPDATE segmentation_profiles SET source_language = coalesce(source_languages[1], 'und');
ALTER TABLE segmentation_profiles ALTER COLUMN source_language DROP DEFAULT;
ALTER TABLE segmentation_profiles ADD CONSTRAINT segmentation_profiles_source_language_check
    CHECK (source_language <> '');
ALTER TABLE segmentation_profiles DROP COLUMN source_languages;

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
