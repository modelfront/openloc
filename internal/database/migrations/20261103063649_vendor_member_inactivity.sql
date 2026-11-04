-- +goose Up
-- A vendor org's membership is disabled after a stretch without use, with a
-- warning email first. last_active_at is when the person last used the org
-- (written at most once a day); joining counts as use.
ALTER TABLE memberships
    ADD COLUMN last_active_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN inactivity_warned_at timestamptz,
    ADD COLUMN disabled_at timestamptz;

-- The inactivity sweep reads the memberships still enabled, oldest use first.
CREATE INDEX idx_memberships_active_since ON memberships (last_active_at) WHERE disabled_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_memberships_active_since;
ALTER TABLE memberships
    DROP COLUMN IF EXISTS disabled_at,
    DROP COLUMN IF EXISTS inactivity_warned_at,
    DROP COLUMN IF EXISTS last_active_at;
