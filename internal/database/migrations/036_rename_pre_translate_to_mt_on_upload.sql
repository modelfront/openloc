-- +goose Up
-- Rename settings_profiles.pre_translate_on_upload → mt_on_upload.
--
-- Migration 033 narrowed the column's semantic from "do everything on upload"
-- to "do MT on upload" but kept the old name for back-compat.
-- Now that the sibling columns tm_on_upload (033) and predict_on_upload (035)
-- exist, the asymmetry — two columns named for what they do and one named for
-- what it historically did — is itself the source of confusion. Rename now,
-- as a dedicated single-purpose migration.
--
-- Postgres metadata-only DDL: ACCESS EXCLUSIVE lock for milliseconds, no
-- data movement. No CHECK constraints reference this column (the only one
-- was dropped in migration 034).

ALTER TABLE settings_profiles RENAME COLUMN pre_translate_on_upload TO mt_on_upload;

-- +goose Down
ALTER TABLE settings_profiles RENAME COLUMN mt_on_upload TO pre_translate_on_upload;
