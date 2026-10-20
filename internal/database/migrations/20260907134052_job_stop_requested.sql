-- +goose Up
-- +goose StatementBegin
-- Cooperative stop flag for a running pipeline job. NULL = no request. The
-- progress writers read it at every chunk boundary and cancel the run
-- context; the run then ends as status = 'stopped' with its saved work kept.
ALTER TABLE jobs ADD COLUMN stop_requested_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE jobs DROP COLUMN stop_requested_at;
-- +goose StatementEnd
