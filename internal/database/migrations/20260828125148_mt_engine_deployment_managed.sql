-- +goose Up
-- Mark the rows the DEPLOYMENT provisioned, as opposed to ones a user made.
--
-- The lock on built-in engines was first written as a check on engine_type, so
-- every `mock` and `libretranslate` row became undeletable — including the ones
-- users and tests create by hand. Ownership is a property of the ROW, not of
-- the type: a hand-made mock engine is still the user's to delete.
--
-- Backfilled false: existing rows were all user-made or seeded, and the startup
-- reconciliation adopts the ones it recognises on the next boot.
ALTER TABLE mt_engines
    ADD COLUMN deployment_managed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE mt_engines DROP COLUMN deployment_managed;
