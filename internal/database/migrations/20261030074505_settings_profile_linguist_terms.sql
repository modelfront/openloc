-- +goose Up
-- +goose StatementBegin

-- "Linguists can add terms" also decides the status linguists' terms get:
-- 'off' (they may not add), 'pending' (a project manager approves them) or
-- 'approved'. Replaces the on/off linguists_add_terms: on becomes pending,
-- off stays off. Written to also run on a database that already has
-- linguist_terms and no linguists_add_terms.

ALTER TABLE settings_profiles
    ADD COLUMN IF NOT EXISTS linguist_terms text NOT NULL DEFAULT 'pending'
    CHECK (linguist_terms IN ('off', 'pending', 'approved'));

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'settings_profiles' AND column_name = 'linguists_add_terms') THEN
        UPDATE settings_profiles SET linguist_terms = 'off' WHERE NOT linguists_add_terms;
    END IF;
END $$;

ALTER TABLE settings_profiles DROP COLUMN IF EXISTS linguists_add_terms;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE settings_profiles
    ADD COLUMN IF NOT EXISTS linguists_add_terms boolean NOT NULL DEFAULT true;

UPDATE settings_profiles SET linguists_add_terms = false WHERE linguist_terms = 'off';

ALTER TABLE settings_profiles DROP COLUMN linguist_terms;

-- +goose StatementEnd
