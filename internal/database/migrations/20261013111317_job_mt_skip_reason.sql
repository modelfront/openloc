-- +goose Up
-- Why the job's last pre-translation asked for MT and got none: the MT router
-- has no engine for the target language (no_route) or an AI engine that can't
-- run it (no_provider). NULL when MT ran, was not set up, was not asked for, or
-- had nothing to do.
ALTER TABLE jobs ADD COLUMN mt_skip_reason TEXT
  CHECK (mt_skip_reason IN ('no_route', 'no_provider'));

-- +goose Down
ALTER TABLE jobs DROP COLUMN mt_skip_reason;
