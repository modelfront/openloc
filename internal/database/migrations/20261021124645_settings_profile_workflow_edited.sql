-- +goose Up
-- True once the profile's steps were changed after its library workflow was
-- applied, so the Workflow tab reads "Based on <workflow> · edited".
ALTER TABLE settings_profiles ADD COLUMN workflow_edited boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE settings_profiles DROP COLUMN workflow_edited;
