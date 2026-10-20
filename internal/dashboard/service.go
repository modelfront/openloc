package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Dashboard reads and chart writes for the MCP tools, on the same queries and
// rules as the REST handlers: every read carries the owner predicate (someone
// else's personal dashboard is pgx.ErrNoRows, like a foreign or deleted one),
// and every write also passes MayWrite.

// Viewer is who is asking.
type Viewer struct {
	TenantID int64
	UserID   int64
	Role     string
}

// Summary is one dashboard in the list.
type Summary struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Personal   bool      `json:"personal"`
	ChartCount int64     `json:"chart_count"`
	CreatedAt  time.Time `json:"created_at"`
}

// Chart is a saved chart: its definition and how to draw it, never data.
type Chart struct {
	ID          int64           `json:"id"`
	DashboardID int64           `json:"dashboard_id"`
	Title       string          `json:"title"`
	Definition  ChartDefinition `json:"definition"`
	Viz         ChartViz        `json:"viz"`
	Controls    []string        `json:"controls"`
	Position    int32           `json:"position"`
}

// Detail is one dashboard with its charts.
type Detail struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Personal  bool      `json:"personal"`
	CanEdit   bool      `json:"can_edit"`
	CreatedAt time.Time `json:"created_at"`
	Charts    []Chart   `json:"charts"`
}

// ErrReadOnly is a write the viewer may not make: a shared dashboard for
// someone who is not owner/admin.
var ErrReadOnly = httputil.NewForbiddenError("only an owner or admin can change a shared dashboard")

// List returns the shared dashboards plus the viewer's own.
func List(ctx context.Context, q db.Querier, v Viewer) ([]Summary, error) {
	rows, err := q.ListDashboards(ctx, db.ListDashboardsParams{TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{ID: r.ID, Name: r.Name, Personal: r.OwnerID != nil, ChartCount: r.ChartCount, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// Get returns one dashboard the viewer may see, with its charts.
func Get(ctx context.Context, q db.Querier, v Viewer, id int64) (Detail, error) {
	d, err := q.GetDashboardTenant(ctx, db.GetDashboardTenantParams{ID: id, TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return Detail{}, err
	}
	rows, err := q.ListDashboardCharts(ctx, db.ListDashboardChartsParams{DashboardID: id, TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return Detail{}, fmt.Errorf("list charts: %w", err)
	}
	out := Detail{ID: d.ID, Name: d.Name, Personal: d.OwnerID != nil, CanEdit: MayWrite(d.OwnerID, v.UserID, v.Role),
		CreatedAt: d.CreatedAt.Time, Charts: make([]Chart, 0, len(rows))}
	for _, r := range rows {
		c, err := toChart(r.ID, d.ID, r.Title, r.Position, r.Definition, r.Viz, r.Controls)
		if err != nil {
			return Detail{}, err
		}
		out.Charts = append(out.Charts, c)
	}
	return out, nil
}

// GetChart returns one chart the viewer may see.
func GetChart(ctx context.Context, q db.Querier, v Viewer, id int64) (Chart, error) {
	r, err := q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{ID: id, TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return Chart{}, err
	}
	return toChart(r.ID, r.DashboardID, r.Title, r.Position, r.Definition, r.Viz, r.Controls)
}

// writableChart loads a chart and applies MayWrite through its dashboard.
func writableChart(ctx context.Context, q db.Querier, v Viewer, id int64) (Chart, error) {
	c, err := GetChart(ctx, q, v, id)
	if err != nil {
		return Chart{}, err
	}
	d, err := q.GetDashboardTenant(ctx, db.GetDashboardTenantParams{ID: c.DashboardID, TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return Chart{}, err
	}
	if !MayWrite(d.OwnerID, v.UserID, v.Role) {
		return Chart{}, ErrReadOnly
	}
	return c, nil
}

// PlanUpdateChart validates an update without storing it: the chart as it is
// and the built replacement. Filter values frozen on the chart survive, like
// the builder's edit (HandleUpdateChart).
func PlanUpdateChart(ctx context.Context, q db.Querier, v Viewer, id int64, spec ChartSpec) (Chart, BuiltChart, error) {
	cur, err := writableChart(ctx, q, v, id)
	if err != nil {
		return Chart{}, BuiltChart{}, err
	}
	built, err := BuildChart(spec, cur.Definition.Filters)
	if err != nil {
		return Chart{}, BuiltChart{}, err
	}
	return cur, built, nil
}

// UpdateChart re-states a chart, as the builder does.
func UpdateChart(ctx context.Context, q db.Querier, v Viewer, id int64, spec ChartSpec) (Chart, error) {
	_, built, err := PlanUpdateChart(ctx, q, v, id, spec)
	if err != nil {
		return Chart{}, err
	}
	n, err := q.UpdateDashboardChart(ctx, db.UpdateDashboardChartParams{
		ID: id, TenantID: v.TenantID, UserID: &v.UserID, Title: built.Title,
		Definition: built.Definition, Viz: built.Viz, Controls: built.Controls,
	})
	if err != nil {
		return Chart{}, fmt.Errorf("update chart: %w", err)
	}
	if n == 0 {
		return Chart{}, pgx.ErrNoRows
	}
	return GetChart(ctx, q, v, id)
}

// DeleteChart soft-deletes a chart (and revokes its share link), as the REST
// delete does.
func DeleteChart(ctx context.Context, q db.Querier, v Viewer, id int64) error {
	if _, err := writableChart(ctx, q, v, id); err != nil {
		return err
	}
	n, err := q.SoftDeleteDashboardChart(ctx, db.SoftDeleteDashboardChartParams{ID: id, TenantID: v.TenantID, UserID: &v.UserID})
	if err != nil {
		return fmt.Errorf("delete chart: %w", err)
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// PreviewDeleteChart checks a delete the viewer may make and returns the chart.
func PreviewDeleteChart(ctx context.Context, q db.Querier, v Viewer, id int64) (Chart, error) {
	return writableChart(ctx, q, v, id)
}

func toChart(id, dashboardID int64, title string, position int32, defRaw, vizRaw, controlsRaw []byte) (Chart, error) {
	var def ChartDefinition
	if err := json.Unmarshal(defRaw, &def); err != nil {
		return Chart{}, fmt.Errorf("chart %d definition: %w", id, err)
	}
	var viz ChartViz
	if err := json.Unmarshal(vizRaw, &viz); err != nil {
		return Chart{}, fmt.Errorf("chart %d viz: %w", id, err)
	}
	var controls FilterConfig
	if len(controlsRaw) > 0 {
		if err := json.Unmarshal(controlsRaw, &controls); err != nil {
			return Chart{}, fmt.Errorf("chart %d controls: %w", id, err)
		}
	}
	if controls.Controls == nil {
		controls.Controls = []string{}
	}
	return Chart{ID: id, DashboardID: dashboardID, Title: title, Definition: def, Viz: viz, Controls: controls.Controls, Position: position}, nil
}

// IsReadOnly reports whether err is a refused write on a shared dashboard.
func IsReadOnly(err error) bool {
	return errors.Is(err, ErrReadOnly)
}
