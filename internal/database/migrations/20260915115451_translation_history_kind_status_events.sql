-- +goose Up
-- Status events in the segment history.
--
-- `translation_history` recorded text changes only, so approval was invisible:
-- a confirm that changed nothing wrote a `human` row showing unchanged text,
-- and QE/APE auto-approving without a rewrite wrote nothing at all.
--
-- The three new kinds carry no `text`. That is deliberate — the edit-distance
-- baseline (`GetLatestHistoryTextForTranslation`) selects the newest row
-- `WHERE text IS NOT NULL`, so a status event can never become the baseline and
-- flatten the next Δ to zero.
--
-- Safe inside goose's transaction: PostgreSQL 12+ allows ADD VALUE in one, as
-- long as the new value is not USED in the same transaction. Nothing here
-- inserts.
ALTER TYPE translation_history_kind ADD VALUE 'approved';
ALTER TYPE translation_history_kind ADD VALUE 'auto_approved';
ALTER TYPE translation_history_kind ADD VALUE 'unconfirmed';

-- +goose Down
-- Enum values cannot be dropped, so the type is rebuilt. The rows carrying the
-- three kinds go with it: they are status events, and nothing derives the
-- segment's current status from them (that lives on translations.status), so
-- dropping them loses audit detail but no state. Same swap shape as
-- 040_collapse_status_enum.sql.
DELETE FROM translation_history
WHERE kind IN ('approved', 'auto_approved', 'unconfirmed');

CREATE TYPE translation_history_kind_old AS ENUM (
    'tm', 'mt', 'ape', 'human', 'propagation', 'file_import', 'cleared'
);

ALTER TABLE translation_history
    ALTER COLUMN kind TYPE translation_history_kind_old
    USING kind::text::translation_history_kind_old;

DROP TYPE translation_history_kind;
ALTER TYPE translation_history_kind_old RENAME TO translation_history_kind;
