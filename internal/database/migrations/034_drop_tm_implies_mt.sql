-- +goose Up
-- Drop the tm_on_upload-implies-pre_translate_on_upload CHECK constraint.
--
-- TM-on-upload and MT-on-upload are independent toggles. The rule added
-- in migration 033 beat the purpose:
-- TM-only auto-upload is a legitimate "fill known matches, leave the rest
-- for humans" workflow that the previous rule disallowed.
--
-- Existing data satisfies the constraint already, so no backfill is needed.

ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tm_implies_mt;

-- +goose Down
ALTER TABLE settings_profiles
    ADD CONSTRAINT settings_profiles_tm_implies_mt
    CHECK (NOT tm_on_upload OR pre_translate_on_upload);
