-- +goose Up
-- An org's default filter profile per format: the pick a new project or
-- template starts with when no template gives one. A built-in or one of the
-- org's profiles (one id sequence across both tables, so no foreign key). No
-- row: the format's built-in Default.
CREATE TABLE filter_profile_defaults (
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    format text NOT NULL CHECK (format IN ('html', 'json_i18n', 'po', 'txt', 'xlsx', 'xml')),
    filter_profile_id bigint NOT NULL,
    updated_by bigint REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, format)
);

-- +goose Down
DROP TABLE filter_profile_defaults;
