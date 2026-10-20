-- +goose Up
-- Add `predict_on_upload` to settings_profiles for the third pipeline phase
-- (ModelFront QP+APE). Independent of TM/MT-on-upload at the schema level
-- (no dependency rule between the flags). The UI gates the
-- checkbox on MT being enabled (predict-without-MT is a no-op since there's
-- nothing to evaluate), but the backend permits any combination and the
-- worker simply skips predict for files with no MT-filled segments.
--
-- Defaults to false: predict requires a configured ModelFront API key (per-
-- tenant), so auto-on-upload would silently no-op for tenants that haven't
-- wired ModelFront yet. Opt-in keeps the surface honest.

ALTER TABLE settings_profiles
    ADD COLUMN predict_on_upload BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE settings_profiles DROP COLUMN predict_on_upload;
