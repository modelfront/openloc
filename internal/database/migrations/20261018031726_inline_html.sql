-- +goose Up
-- +goose StatementBegin

-- "Strings contain HTML" for JSON i18n, PO/POT, TXT and XLSX files. On
-- settings_profiles so templates carry it; on files so a file is exported and
-- checked in the mode it was parsed with, whatever the setting says later.
-- DEFAULT true backfills existing rows with the HTML reading they were parsed
-- with.

ALTER TABLE settings_profiles
    ADD COLUMN inline_html boolean NOT NULL DEFAULT true;

ALTER TABLE files
    ADD COLUMN inline_html boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE files DROP COLUMN inline_html;
ALTER TABLE settings_profiles DROP COLUMN inline_html;

-- +goose StatementEnd
