-- The Pinned view: a person's own dashboards of references to charts. Every
-- query is scoped by tenant AND user; a chart reference is only read through a
-- chart the person may still see (live, on a live dashboard that is shared or
-- theirs). Read back, follows_source is true only while the source is such a
-- dashboard, so a pinned dashboard never follows one the person cannot open.

-- name: ListPinnedDashboards :many
SELECT pd.id, pd.name, pd.source_dashboard_id,
       (pd.follows_source AND EXISTS (
           SELECT 1 FROM dashboards d
           WHERE d.id = pd.source_dashboard_id AND d.tenant_id = pd.tenant_id AND d.deleted_at IS NULL
             AND (d.owner_id IS NULL OR d.owner_id = @user_id::bigint)))::bool AS follows_source,
       pd.filters, pd.position, pd.created_at
FROM pinned_dashboards pd
WHERE pd.tenant_id = @tenant_id AND pd.user_id = @user_id AND pd.deleted_at IS NULL
ORDER BY pd.position, pd.id;

-- name: GetPinnedDashboard :one
SELECT pd.id, pd.name, pd.source_dashboard_id,
       (pd.follows_source AND EXISTS (
           SELECT 1 FROM dashboards d
           WHERE d.id = pd.source_dashboard_id AND d.tenant_id = pd.tenant_id AND d.deleted_at IS NULL
             AND (d.owner_id IS NULL OR d.owner_id = @user_id::bigint)))::bool AS follows_source,
       pd.filters, pd.position, pd.created_at
FROM pinned_dashboards pd
WHERE pd.id = @id AND pd.tenant_id = @tenant_id AND pd.user_id = @user_id AND pd.deleted_at IS NULL;

-- name: GetPinnedDashboardForSource :one
SELECT pd.id, pd.name, pd.source_dashboard_id,
       (pd.follows_source AND EXISTS (
           SELECT 1 FROM dashboards d
           WHERE d.id = pd.source_dashboard_id AND d.tenant_id = pd.tenant_id AND d.deleted_at IS NULL
             AND (d.owner_id IS NULL OR d.owner_id = @user_id::bigint)))::bool AS follows_source,
       pd.filters, pd.position, pd.created_at
FROM pinned_dashboards pd
WHERE pd.tenant_id = @tenant_id AND pd.user_id = @user_id AND pd.source_dashboard_id = @source_dashboard_id
  AND pd.deleted_at IS NULL;

-- name: CreatePinnedDashboard :one
INSERT INTO pinned_dashboards (tenant_id, user_id, name, source_dashboard_id, follows_source, filters, position)
VALUES (@tenant_id, @user_id, @name, @source_dashboard_id, @follows_source, @filters,
        (SELECT COALESCE(MAX(position), 0) + 1 FROM pinned_dashboards
         WHERE tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL))
RETURNING id;

-- name: SetPinnedDashboardFollows :exec
UPDATE pinned_dashboards SET follows_source = @follows_source
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL;

-- name: RenamePinnedDashboard :execrows
UPDATE pinned_dashboards SET name = @name
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL;

-- name: UpdatePinnedDashboardFilters :execrows
UPDATE pinned_dashboards SET filters = @filters
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL;

-- name: ReorderPinnedDashboard :execrows
UPDATE pinned_dashboards SET position = @position
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL;

-- name: DeletePinnedChartsOfPinned :exec
-- A pinned dashboard's references, removed before it is, so each chart can be
-- pinned again elsewhere.
DELETE FROM pinned_charts pc
USING pinned_dashboards pd
WHERE pc.pinned_dashboard_id = pd.id AND pd.id = @id
  AND pd.tenant_id = @tenant_id AND pd.user_id = @user_id;

-- name: SoftDeletePinnedDashboard :exec
UPDATE pinned_dashboards SET deleted_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND deleted_at IS NULL;

-- name: ListPinnedCharts :many
-- The charts a person has pinned, in order: their references, then — for a
-- pinned dashboard that follows its source — the source's charts that are not
-- referenced anywhere yet. pinned_id = 0 lists every pinned dashboard's.
WITH refs AS (
    SELECT pc.pinned_dashboard_id, pc.chart_id, pc.position, 0 AS tier
    FROM pinned_charts pc
    JOIN pinned_dashboards pd ON pd.id = pc.pinned_dashboard_id
    WHERE pd.tenant_id = @tenant_id AND pd.user_id = @user_id AND pd.deleted_at IS NULL
      AND (@pinned_id::bigint = 0 OR pd.id = @pinned_id::bigint)
    UNION ALL
    SELECT pd.id, c.id, c.position, 1
    FROM pinned_dashboards pd
    JOIN dashboard_charts c ON c.dashboard_id = pd.source_dashboard_id AND c.deleted_at IS NULL
    WHERE pd.tenant_id = @tenant_id AND pd.user_id = @user_id AND pd.deleted_at IS NULL
      AND pd.follows_source
      AND (@pinned_id::bigint = 0 OR pd.id = @pinned_id::bigint)
      AND NOT EXISTS (SELECT 1 FROM pinned_charts x WHERE x.user_id = @user_id AND x.chart_id = c.id)
)
SELECT r.pinned_dashboard_id, (r.tier = 0)::bool AS referenced,
       c.id, c.title, c.definition, c.viz, c.controls,
       d.id AS dashboard_id, d.name AS dashboard_name, (d.owner_id IS NOT NULL)::bool AS dashboard_personal
FROM refs r
JOIN dashboard_charts c ON c.id = r.chart_id AND c.tenant_id = @tenant_id AND c.deleted_at IS NULL
JOIN dashboards d ON d.id = c.dashboard_id AND d.deleted_at IS NULL
                 AND (d.owner_id IS NULL OR d.owner_id = @user_id)
ORDER BY r.pinned_dashboard_id, r.tier, r.position, c.id;

-- name: ListVisibleChartDashboards :many
-- Every chart the person can see, with its dashboard: what "is this dashboard
-- pinned" is decided against.
SELECT c.id, c.dashboard_id
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id AND d.deleted_at IS NULL
                 AND (d.owner_id IS NULL OR d.owner_id = @user_id)
WHERE c.tenant_id = @tenant_id AND c.deleted_at IS NULL;

-- name: GetPinnedChartRef :one
SELECT pc.pinned_dashboard_id
FROM pinned_charts pc
JOIN pinned_dashboards pd ON pd.id = pc.pinned_dashboard_id
WHERE pc.user_id = @user_id AND pc.chart_id = @chart_id
  AND pd.tenant_id = @tenant_id AND pd.deleted_at IS NULL;

-- name: AddPinnedChart :exec
-- Appended after the pinned dashboard's last reference.
INSERT INTO pinned_charts (pinned_dashboard_id, user_id, chart_id, position)
VALUES (@pinned_dashboard_id, @user_id, @chart_id,
        (SELECT COALESCE(MAX(position), 0) + 1 FROM pinned_charts WHERE pinned_dashboard_id = @pinned_dashboard_id))
ON CONFLICT (user_id, chart_id) DO NOTHING;

-- name: DeletePinnedChart :exec
DELETE FROM pinned_charts pc
USING pinned_dashboards pd
WHERE pc.pinned_dashboard_id = pd.id AND pc.user_id = @user_id AND pc.chart_id = @chart_id
  AND pd.tenant_id = @tenant_id;

-- name: DeletePinnedChartsOfDashboard :exec
-- Unpins every chart of one source dashboard, wherever it was pinned.
DELETE FROM pinned_charts pc
USING pinned_dashboards pd, dashboard_charts c
WHERE pc.pinned_dashboard_id = pd.id AND pc.chart_id = c.id
  AND pc.user_id = @user_id AND pd.tenant_id = @tenant_id
  AND c.dashboard_id = @dashboard_id;

-- name: MovePinnedChart :execrows
UPDATE pinned_charts pc
SET pinned_dashboard_id = @to_pinned_id,
    position = (SELECT COALESCE(MAX(position), 0) + 1 FROM pinned_charts WHERE pinned_dashboard_id = @to_pinned_id)
FROM pinned_dashboards pd
WHERE pc.pinned_dashboard_id = pd.id AND pc.user_id = @user_id AND pc.chart_id = @chart_id
  AND pd.tenant_id = @tenant_id;

-- name: ReorderPinnedChart :exec
UPDATE pinned_charts SET position = @position
WHERE pinned_dashboard_id = @pinned_dashboard_id AND user_id = @user_id AND chart_id = @chart_id;

-- name: CountPinnedUsersOfSharedDashboards :one
-- How many people would lose pinned charts if the shared dashboards went:
-- what the reset dialog warns about.
SELECT count(DISTINCT pd.user_id)::bigint
FROM pinned_dashboards pd
LEFT JOIN pinned_charts pc ON pc.pinned_dashboard_id = pd.id
LEFT JOIN dashboard_charts c ON c.id = pc.chart_id AND c.deleted_at IS NULL
JOIN dashboards d ON d.deleted_at IS NULL AND d.owner_id IS NULL
                 AND (d.id = c.dashboard_id OR (pd.follows_source AND d.id = pd.source_dashboard_id))
WHERE pd.tenant_id = @tenant_id AND pd.deleted_at IS NULL;
