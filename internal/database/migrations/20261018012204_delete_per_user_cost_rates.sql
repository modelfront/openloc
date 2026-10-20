-- +goose Up
-- Per-translator pay rates have no reader, writer or delete path, so they are
-- removed rather than kept indefinitely. Org-wide rates (user_id IS NULL) stay.
DELETE FROM cost_rates WHERE user_id IS NOT NULL;

-- +goose Down
-- The deleted rows cannot be restored; the schema is unchanged.
SELECT 1;
