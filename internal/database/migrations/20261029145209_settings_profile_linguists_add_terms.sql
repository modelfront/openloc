-- +goose Up
-- +goose StatementBegin

-- Whether linguists may add terms from the CAT editor (always saved as
-- pending). On settings_profiles so templates carry it. DEFAULT true backfills
-- existing projects and templates with the setting on.

ALTER TABLE settings_profiles
    ADD COLUMN linguists_add_terms boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE settings_profiles DROP COLUMN linguists_add_terms;

-- +goose StatementEnd
