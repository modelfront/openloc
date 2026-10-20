-- +goose NO TRANSACTION
-- +goose Up
-- Who confirmed an approved segment: a person in the editor, or the assistant
-- on a user's request. Assistant-confirmed segments are never written to
-- translation memory; a later human confirm resets this to 'user'.
--
-- Each statement commits on its own. The column add and the NOT VALID check
-- only touch the catalog, so their ACCESS EXCLUSIVE locks are brief; VALIDATE
-- scans the table under SHARE UPDATE EXCLUSIVE, which lets reads and writes
-- through. The IF EXISTS / IF NOT EXISTS guards make a re-run after a partial
-- failure safe.
ALTER TABLE translations ADD COLUMN IF NOT EXISTS confirmed_via text NOT NULL DEFAULT 'user';

ALTER TABLE translations DROP CONSTRAINT IF EXISTS translations_confirmed_via_check;

ALTER TABLE translations
    ADD CONSTRAINT translations_confirmed_via_check CHECK (confirmed_via IN ('user', 'assistant')) NOT VALID;

ALTER TABLE translations VALIDATE CONSTRAINT translations_confirmed_via_check;

-- +goose Down
ALTER TABLE translations DROP COLUMN IF EXISTS confirmed_via;
