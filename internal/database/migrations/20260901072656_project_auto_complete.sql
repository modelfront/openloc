-- +goose Up
-- +goose StatementBegin

-- Auto-completion opt-out: when every job in a project is 'completed', the
-- project's status flips to 'completed' (and back to 'in_progress' when that
-- stops holding).
--
-- On settings_profiles rather than projects so templates carry it: a template
-- is a name plus a settings_profile, and both copy paths (ApplyTemplate,
-- save-as-template) already carry every profile scalar. "Always auto-complete
-- my projects" is an org-level preference, so it belongs in the reusable bag.
--
-- DEFAULT true applies to new rows and backfills existing ones — the feature
-- is on by default, matching the option's intent.

ALTER TABLE settings_profiles
    ADD COLUMN auto_complete boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE settings_profiles DROP COLUMN auto_complete;

-- +goose StatementEnd
