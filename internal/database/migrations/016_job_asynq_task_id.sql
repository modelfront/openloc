-- +goose Up
ALTER TABLE jobs ADD COLUMN asynq_task_id text;

-- +goose Down
ALTER TABLE jobs DROP COLUMN IF EXISTS asynq_task_id;
