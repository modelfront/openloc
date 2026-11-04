-- +goose Up
-- +goose StatementBegin

-- Filter profiles: named import options for one file format. The built-ins
-- OpenLoc ships and each org's own take their ids from one sequence, so an id
-- names one profile across the two tables and filter_profile_catalog lists
-- them together. format is a filter format: 'po' covers .po and .pot.
CREATE SEQUENCE filter_profile_id_seq;

-- The built-ins: read-only, the same for every org, never changed once
-- released (a change is a new row with the next version in builtin_key).
CREATE TABLE filter_builtin_profiles (
    id bigint PRIMARY KEY DEFAULT nextval('filter_profile_id_seq'),
    -- "builtin:<format>-<name>@<version>".
    builtin_key text NOT NULL UNIQUE,
    name text NOT NULL,
    format text NOT NULL CHECK (format IN ('html', 'json_i18n', 'po', 'txt', 'xlsx', 'xml')),
    description text NOT NULL,
    options jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- An org's own profiles; options is filterprofile.Options. Soft-deleted:
-- live reads filter deleted_at IS NULL.
CREATE TABLE filter_profiles (
    id bigint PRIMARY KEY DEFAULT nextval('filter_profile_id_seq'),
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    format text NOT NULL CHECK (format IN ('html', 'json_i18n', 'po', 'txt', 'xlsx', 'xml')),
    options jsonb NOT NULL,
    -- The profile this one was copied from, for display only.
    based_on bigint,
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX filter_profiles_name_idx
    ON filter_profiles (tenant_id, format, lower(name))
    WHERE deleted_at IS NULL;

-- Both kinds in one shape for reads; tenant_id is NULL on a built-in.
CREATE VIEW filter_profile_catalog AS
SELECT id, NULL::bigint AS tenant_id, builtin_key, name, format, description,
       options, NULL::bigint AS based_on,
       created_at, created_at AS updated_at, NULL::timestamptz AS deleted_at
FROM filter_builtin_profiles
UNION ALL
SELECT id, tenant_id, NULL::text, name, format, ''::text,
       options, based_on,
       created_at, updated_at, deleted_at
FROM filter_profiles;

-- The built-ins. Their options are stored normalized, and every format has a
-- Default (TestIntegrationFilterBuiltins).
INSERT INTO filter_builtin_profiles (builtin_key, name, format, description, options) VALUES
    ('builtin:html-default@1', 'Default', 'html', 'Text, image and link titles, form placeholders and the meta description. Code in <pre> stays as is.', '{}'),
    ('builtin:xml-default@1', 'Default', 'xml', 'Every element with text. translate="no" and translatable="false" skip an element.', '{}'),
    ('builtin:json_i18n-default@1', 'Default', 'json_i18n', 'Tags in strings become protected tags.', '{"inline_html": true}'),
    ('builtin:json_i18n-plain@1', 'Plain text strings', 'json_i18n', 'Strings are plain text: no tags, exported as written.', '{"inline_html": false}'),
    ('builtin:po-default@1', 'Default', 'po', 'Tags in strings become protected tags.', '{"inline_html": true}'),
    ('builtin:po-plain@1', 'Plain text strings', 'po', 'Strings are plain text: no tags, exported as written.', '{"inline_html": false}'),
    ('builtin:txt-default@1', 'Default', 'txt', 'Tags in lines become protected tags.', '{"inline_html": true}'),
    ('builtin:txt-plain@1', 'Plain text strings', 'txt', 'Lines are plain text: no tags, exported as written.', '{"inline_html": false}'),
    ('builtin:xlsx-default@1', 'Default', 'xlsx', 'Columns are read from row 1. Tags in cells become protected tags.', '{"inline_html": true}'),
    ('builtin:xlsx-plain@1', 'Plain text strings', 'xlsx', 'Columns are read from row 1. Cells are plain text: no tags, exported as written.', '{"inline_html": false}');

-- The filter profile a project or template uses per format. No row: the
-- format's Default built-in. filter_profile_id names a built-in or an org
-- profile, so it has no foreign key (as segmentation_profile_id).
CREATE TABLE settings_profile_filter_profiles (
    settings_profile_id bigint NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
    format text NOT NULL CHECK (format IN ('html', 'json_i18n', 'po', 'txt', 'xlsx', 'xml')),
    filter_profile_id bigint NOT NULL,
    PRIMARY KEY (settings_profile_id, format)
);
CREATE INDEX settings_profile_filter_profiles_profile_idx
    ON settings_profile_filter_profiles (filter_profile_id);

-- "Strings contain HTML" off becomes the Plain text strings built-in of each
-- format it applied to.
INSERT INTO settings_profile_filter_profiles (settings_profile_id, format, filter_profile_id)
SELECT sp.id, b.format, b.id
FROM settings_profiles sp
JOIN filter_builtin_profiles b ON b.builtin_key IN
    ('builtin:json_i18n-plain@1', 'builtin:po-plain@1', 'builtin:txt-plain@1', 'builtin:xlsx-plain@1')
WHERE NOT sp.inline_html;

ALTER TABLE settings_profiles DROP COLUMN inline_html;

-- The filter profile a file was imported with, in full
-- (filterprofile.Snapshot), so every later parse reads it the same way. NULL:
-- the file was stored before filter profiles and uses the defaults.
ALTER TABLE files ADD COLUMN filter_profile jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE files DROP COLUMN filter_profile;

ALTER TABLE settings_profiles ADD COLUMN inline_html boolean NOT NULL DEFAULT true;
-- An org profile has no "plain" key, so its options decide.
UPDATE settings_profiles sp SET inline_html = false
WHERE EXISTS (
    SELECT 1 FROM settings_profile_filter_profiles l
    JOIN filter_profile_catalog c ON c.id = l.filter_profile_id
    WHERE l.settings_profile_id = sp.id
      AND l.format IN ('json_i18n', 'po', 'txt', 'xlsx')
      AND (c.options->>'inline_html')::boolean IS FALSE);

DROP TABLE settings_profile_filter_profiles;
DROP VIEW filter_profile_catalog;
DROP TABLE filter_profiles;
DROP TABLE filter_builtin_profiles;
DROP SEQUENCE filter_profile_id_seq;

-- +goose StatementEnd
