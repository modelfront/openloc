-- Dashboards. Charts store query definitions, not data.
-- Every query is tenant-scoped directly, and reads only live rows: deleting a
-- dashboard or chart sets deleted_at, so every read and
-- write carries `deleted_at IS NULL` for each table it touches.

-- name: ListDashboards :many
-- Shared dashboards plus the caller's own. A personal dashboard belongs to one
-- user, so the owner predicate is a visibility boundary, not a filter — every
-- read of this table carries it.
SELECT d.id, d.name, d.owner_id, d.created_at,
       COUNT(c.id) AS chart_count
FROM dashboards d
LEFT JOIN dashboard_charts c ON c.dashboard_id = d.id AND c.deleted_at IS NULL
WHERE d.tenant_id = @tenant_id AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id)
GROUP BY d.id
-- position first, name as the tiebreaker. New rows append (CreateDashboard);
-- the tiebreak only orders rows that predate positions, which all hold 0.
ORDER BY d.owner_id NULLS FIRST, d.position, d.name;

-- name: ListDashboardPreviewCharts :many
-- The first few charts of every dashboard the caller may see, in the order the
-- dashboard shows them, for the list page's previews. Same visibility predicate
-- as ListDashboards: a preview is a read of the chart.
SELECT ranked.dashboard_id, ranked.id, ranked.title, ranked.viz
FROM (
    SELECT c.dashboard_id, c.id, c.title, c.viz, c.position,
           row_number() OVER (PARTITION BY c.dashboard_id ORDER BY c.position, c.id) AS rank
    FROM dashboard_charts c
    JOIN dashboards d ON d.id = c.dashboard_id
    WHERE c.tenant_id = @tenant_id AND d.tenant_id = @tenant_id
      AND c.deleted_at IS NULL AND d.deleted_at IS NULL
      AND (d.owner_id IS NULL OR d.owner_id = @user_id)
) ranked
WHERE ranked.rank <= @per_dashboard::bigint
ORDER BY ranked.dashboard_id, ranked.rank;

-- name: GetDashboardTenant :one
-- Someone else's personal dashboard is indistinguishable from one that doesn't
-- exist: the caller gets the same 404 either way.
SELECT id, tenant_id, name, owner_id, filters, created_by, created_at
FROM dashboards
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND (owner_id IS NULL OR owner_id = @user_id);

-- name: UpdateDashboardFilters :execrows
-- Replaces the whole control list; the caller validates it against the closed
-- set first (internal/dashboard/filters.go). The handler checks the role/owner
-- gate before calling — this predicate only keeps the write tenant-scoped.
UPDATE dashboards SET filters = @filters WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL;

-- name: RenameDashboard :execrows
-- The handler checks the role/owner gate first; this predicate only keeps the
-- write tenant-scoped. A name already taken on this dashboard's side surfaces
-- as a unique violation from the partial indexes, which the handler turns into
-- a 409 — a pre-check would race.
UPDATE dashboards SET name = @name WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL;

-- name: RenameDashboardChart :execrows
-- Joined through dashboards for the owner predicate, like every other chart
-- write: a chart id is a small integer, so tenant scoping alone would let
-- anyone in the org rename a colleague's personal chart by guessing one.
UPDATE dashboard_charts c SET title = @title
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: SetDashboardOwner :execrows
-- Moves a dashboard between Shared (NULL) and one user's own. Name collisions
-- surface as a unique violation, which the handler turns into a 409.
UPDATE dashboards SET owner_id = @owner_id WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL;

-- name: FindDashboardByName :one
-- Case-insensitive: create_chart resolves "the qa dashboard" by name.
--
-- The caller's OWN dashboard wins over a shared one of the same name: "add it
-- to Operations" means the Operations they have been working in. Falling back
-- to the shared one keeps "add to <the org's dashboard>" working without
-- spawning a private duplicate beside it.
SELECT id, tenant_id, name, owner_id, created_by, created_at
FROM dashboards
WHERE tenant_id = @tenant_id AND LOWER(name) = LOWER(@name) AND deleted_at IS NULL
  AND (owner_id IS NULL OR owner_id = @user_id)
ORDER BY owner_id NULLS LAST
LIMIT 1;

-- name: CreateDashboard :one
-- owner_id NULL creates a shared dashboard; non-NULL a personal one. Appended
-- to the end of ITS side, like a chart on a dashboard: left at 0 it would sort
-- by name among the never-dragged ones, so the built-in set would come out
-- alphabetical ("AI" before "Overview") instead of in its declared order.
INSERT INTO dashboards (tenant_id, name, created_by, owner_id, position)
VALUES (@tenant_id, @name, @created_by, @owner_id,
        (SELECT COALESCE(MAX(position), 0) + 1 FROM dashboards
         WHERE tenant_id = @tenant_id AND owner_id IS NOT DISTINCT FROM @owner_id))
RETURNING id, tenant_id, name, owner_id, created_by, created_at;

-- name: SoftDeleteDashboard :execrows
-- A dashboard is a user entity, so deleting one marks it.
-- Its charts stay as they are: every chart read joins through a live
-- dashboard, so they go dark with it, share links included.
UPDATE dashboards SET deleted_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND (owner_id IS NULL OR owner_id = @user_id);

-- name: ListDashboardCharts :many
SELECT c.id, c.dashboard_id, c.title, c.definition, c.viz, c.controls, c.position, c.created_at
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id
WHERE c.dashboard_id = @dashboard_id AND c.tenant_id = @tenant_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id)
ORDER BY c.position, c.id;

-- name: GetDashboardChartTenant :one
-- Joined through dashboards for the owner predicate: a chart id is a small
-- integer, so tenant scoping alone would let anyone in the org read a
-- colleague's personal dashboard by guessing one.
SELECT c.id, c.dashboard_id, c.title, c.definition, c.viz, c.controls, c.position, c.created_at
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id
WHERE c.id = @id AND c.tenant_id = @tenant_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: CreateDashboardChart :one
INSERT INTO dashboard_charts (tenant_id, dashboard_id, title, definition, viz, controls, created_by, position)
VALUES (@tenant_id, @dashboard_id, @title, @definition, @viz,
        COALESCE(sqlc.narg('controls')::jsonb, '{"controls": []}'::jsonb), @created_by,
        (SELECT COALESCE(MAX(position), 0) + 1 FROM dashboard_charts WHERE dashboard_id = @dashboard_id))
RETURNING id, dashboard_id, title, definition, viz, position, created_at;

-- name: UpdateDashboardChart :execrows
-- Re-states a chart from the builder. Joined through dashboards for the owner
-- predicate like every other chart write; the handler checks mayWrite first.
UPDATE dashboard_charts c
SET title = @title, definition = @definition, viz = @viz, controls = @controls
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: SoftDeleteDashboardChart :execrows
-- A chart is a user entity, so deleting one marks it. Its
-- share link is revoked in the same statement: a deleted chart must not stay
-- readable to whoever holds the URL.
UPDATE dashboard_charts c
SET deleted_at = now(), share_token = NULL, share_created_by = NULL, share_created_at = NULL
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: ListVisibleChartDefinitions :many
-- Titles + definitions of every chart the caller can see, for the chat's
-- suggest-chart endpoint: the model needs to know what already exists so it
-- suggests something new. Owner-scoped like every other read — a colleague's
-- private dashboard is not context the model gets to use.
SELECT c.title, c.definition, c.viz
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id
WHERE c.tenant_id = @tenant_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: MoveDashboardChart :execrows
-- Moves a chart to another dashboard, landing it last. The owner predicate
-- here covers the SOURCE dashboard only; the destination is resolved and
-- checked by the handler (dashboardFor + mayWrite) before this runs.
UPDATE dashboard_charts c
SET dashboard_id = @dashboard_id,
    position = (SELECT COALESCE(MAX(position), 0) + 1
                FROM dashboard_charts WHERE dashboard_id = @dashboard_id)
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: FindSharedDashboardByName :one
-- Shared-only lookup for create_chart's shared path: from the Shared tab,
-- "add it to Operations" means the ORG's Operations, so a personal dashboard of
-- the same name must not shadow it (FindDashboardByName prefers the caller's).
SELECT id, tenant_id, name, owner_id, created_by, created_at
FROM dashboards
WHERE tenant_id = @tenant_id AND LOWER(name) = LOWER(@name) AND owner_id IS NULL AND deleted_at IS NULL;

-- name: ReorderDashboardChart :execrows
-- One chart's new position. Callers send the whole order in a transaction, so a
-- partial failure leaves no gap the read path can trip on (ORDER BY position, id
-- keeps ties stable either way).
UPDATE dashboard_charts c
SET position = @position
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND c.dashboard_id = @dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND d.id = c.dashboard_id AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: SetChartShareToken :execrows
-- Issues (or replaces) a chart's share link. Owner-predicated like every other
-- chart WRITE: only someone who may edit the chart may hand it out.
UPDATE dashboard_charts c
SET share_token = @share_token, share_created_by = @share_created_by, share_created_at = now()
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: RevokeChartShareToken :execrows
-- Clears the link. The URL dies immediately — there is no grace period,
-- because the only reason to revoke is that it reached the wrong person.
UPDATE dashboard_charts c
SET share_token = NULL, share_created_by = NULL, share_created_at = NULL
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: GetChartByShareToken :one
-- THE capability read: resolves a chart by its share token, with NO owner
-- predicate — that is the whole point, since the holder of the link is exactly
-- someone who could not reach the chart by browsing.
--
-- Still tenant-scoped: a token is a grant inside one organisation, never a
-- cross-tenant one. And the token must be non-NULL, so a revoked link cannot
-- match a row whose column was cleared.
SELECT c.id, c.dashboard_id, c.title, c.definition, c.viz, c.controls,
       d.name AS dashboard_name, d.filters AS dashboard_filters
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id
WHERE c.share_token = @share_token AND c.share_token IS NOT NULL
  AND c.tenant_id = @tenant_id AND c.deleted_at IS NULL AND d.deleted_at IS NULL;

-- name: ReorderDashboard :execrows
-- One dashboard's new position. Callers send the whole order in a transaction,
-- like chart reordering, so a partial failure cannot leave two rows claiming
-- one slot. Owner-predicated: the write cannot reach a dashboard the caller
-- can't see, and the handler checks mayWrite on top of that.
UPDATE dashboards SET position = @position
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL
  AND (owner_id IS NULL OR owner_id = @user_id);

-- name: ListDashboardNamesOnSide :many
-- Names already taken on ONE side — shared (owner_id NULL) or one user's own.
-- `IS NOT DISTINCT FROM` because the sides are distinguished by a NULL, which
-- plain `=` never matches.
--
-- Feeds the "Copy of X", "Copy of X 2", … search. The insert still has to cope
-- with a unique violation: two copies racing would both read the same free name.
SELECT name FROM dashboards
WHERE tenant_id = @tenant_id AND deleted_at IS NULL
  AND owner_id IS NOT DISTINCT FROM @owner_id
  AND name LIKE @prefix;

-- name: SoftDeleteSharedDashboards :execrows
-- Marks every live SHARED dashboard for a tenant deleted.
-- Personal ones are untouched: a reset of what the org sees must not reach into
-- anyone's private workspace. Their charts and share links go dark with them,
-- as with SoftDeleteDashboard.
UPDATE dashboards SET deleted_at = now()
WHERE tenant_id = @tenant_id AND owner_id IS NULL AND deleted_at IS NULL;

-- name: CountSharedDashboards :one
-- What a reset would destroy, so the confirmation can name a number instead of
-- asking the user to guess.
SELECT COUNT(*) FROM dashboards WHERE tenant_id = @tenant_id AND owner_id IS NULL AND deleted_at IS NULL;

-- name: UpdateChartControls :execrows
-- Replaces a chart's own control list. Owner-predicated like every chart write;
-- the handler checks mayWrite on the dashboard on top of that.
UPDATE dashboard_charts c SET controls = @controls
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);

-- name: FindChartByTitle :one
-- Resolves a chart the way a person names it: by title, within one dashboard.
-- Titles are not unique — two dashboards may each hold a "Volume" — so the
-- dashboard is part of the key. Case-insensitive, like dashboard lookup.
SELECT c.id, c.dashboard_id, c.title, c.controls
FROM dashboard_charts c
JOIN dashboards d ON d.id = c.dashboard_id
WHERE c.tenant_id = @tenant_id AND c.dashboard_id = @dashboard_id
  AND LOWER(c.title) = LOWER(@title)
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id)
ORDER BY c.position, c.id
LIMIT 1;

-- name: UpdateChartDefinition :execrows
-- Rewrites a chart's stored question and how it is drawn. Used when a control
-- is removed and its current value is frozen into the chart, so the card keeps
-- showing what it showed a moment earlier.
UPDATE dashboard_charts c SET definition = @definition, viz = @viz
FROM dashboards d
WHERE c.id = @id AND c.tenant_id = @tenant_id AND d.id = c.dashboard_id
  AND c.deleted_at IS NULL AND d.deleted_at IS NULL
  AND (d.owner_id IS NULL OR d.owner_id = @user_id);
