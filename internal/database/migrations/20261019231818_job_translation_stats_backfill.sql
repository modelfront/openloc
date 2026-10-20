-- +goose NO TRANSACTION
-- +goose Up
-- Counts every existing job into job_translation_stats, one transaction per
-- batch of files (up to 100 files, about 50k translations; a larger file one
-- language at a time), while the app keeps writing: each batch corrects its
-- jobs against a live recount under the recount lock, so writes the triggers
-- already recorded are not counted twice. Re-running it is safe.
CALL job_translation_stats_correct_all();

-- +goose Down
-- Nothing to undo: the triggers keep the counts right for as long as they
-- exist, and the migration that creates them drops the table.
SELECT 1;
