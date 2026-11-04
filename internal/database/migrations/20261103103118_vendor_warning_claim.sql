-- +goose Up
-- The inactivity sweep claims a warning before emailing it, and sets
-- inactivity_warned_at only once the email went out. A claim whose email never
-- went out expires, so a later sweep warns the person, and nobody is disabled
-- on a warning they did not get.
ALTER TABLE memberships ADD COLUMN inactivity_warning_claimed_at timestamptz;

-- +goose Down
ALTER TABLE memberships DROP COLUMN IF EXISTS inactivity_warning_claimed_at;
