-- +goose Up
-- Per-chart filter controls.
--
-- Same shape as dashboards.filters — {"controls": [...]} — and the same rule:
-- only the control LIST is stored. The SELECTIONS stay in the URL, so a
-- narrowed view is still a shareable link and nothing is saved per user.
--
-- A chart needs its own when it asks a question the rest of the dashboard does
-- not: an AI-engine dropdown is meaningful on a token chart and a dead control
-- on a QA one, so putting it on the dashboard would add a widget that does
-- nothing to most cards.
ALTER TABLE dashboard_charts ADD COLUMN controls jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE dashboard_charts DROP COLUMN controls;
