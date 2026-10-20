-- +goose Up
-- Dashboard-level filter controls: one config per dashboard, applied to every
-- chart on it at render time. Only which controls the dashboard SHOWS lives
-- here — the selected values travel in the URL, so a filtered view is
-- shareable and nothing is stored per user.
--
-- Shape: {"controls": ["date_range", "unit", "granularity", "target_language", ...]}
-- The set is closed and validated in Go (internal/dashboard/filters.go); an
-- unknown control is rejected on write, so a render never has to guess.
ALTER TABLE dashboards
    ADD COLUMN filters jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE dashboards
    DROP COLUMN filters;
