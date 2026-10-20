-- +goose Up
-- Per-step productivity report from translation_history.
--
-- The history table captured edit_distance per event but not the editing time,
-- which only lived cumulatively on translations.editing_time_ms. Per-step
-- words-per-hour needs per-event time, so add it here. Nullable: only human
-- (and propagation) events carry a meaningful delta; automation rows leave it
-- NULL.
ALTER TABLE translation_history
    ADD COLUMN editing_time_ms bigint
        CHECK (editing_time_ms IS NULL OR editing_time_ms >= 0);

-- +goose Down
ALTER TABLE translation_history DROP COLUMN editing_time_ms;
