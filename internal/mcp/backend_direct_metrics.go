package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	"openloc/internal/dashboard"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/metrics"
)

// QueryMetrics runs one semantic-layer aggregate.
//
// Tenant scoping happens inside metrics.Run — the compiler pins tenant_id as
// $1 on the frozen join graph, so unlike the entity tools there is no
// per-resource ownership pre-check to do here: the vocabulary cannot name a
// foreign resource, only filter values that simply won't match.
func (b *directBackend) QueryMetrics(ctx context.Context, in QueryMetricsIn) (metrics.Result, error) {
	// Dynamic aggregate SQL needs the pool; the Querier alone can't run it.
	if err := b.needPool("querying metrics"); err != nil {
		return metrics.Result{}, err
	}
	tenantID, _ := scope(ctx)

	from, err := time.Parse("2006-01-02", in.From)
	if err != nil {
		return metrics.Result{}, invalidf("'from' must be YYYY-MM-DD")
	}
	to, err := time.Parse("2006-01-02", in.To)
	if err != nil {
		return metrics.Result{}, invalidf("'to' must be YYYY-MM-DD")
	}

	res, err := metrics.Run(ctx, b.pool, tenantID, metrics.Request{
		Measure:    in.Measure,
		Dimensions: in.Dimensions,
		Filters:    in.Filters,
		From:       from,
		To:         to,
	})
	if err != nil {
		if errors.Is(err, metrics.ErrValidation) {
			return metrics.Result{}, invalidf("%s", trimMetricsValidation(err))
		}
		return metrics.Result{}, fmt.Errorf("query metrics: %w", err)
	}
	return *res, nil
}

// trimMetricsValidation strips the sentinel prefix so the model sees
// "unknown measure …", not "metrics validation: unknown measure …".
func trimMetricsValidation(err error) string {
	const prefix = "metrics validation: "
	s := err.Error()
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// chartTypes mirrors the chat-layer render_chart whitelist. Duplicated on
// purpose: this one gates what gets PERSISTED, the loop's gates what gets
// drawn ephemerally — they may drift apart (e.g. dashboards gaining a table
// type charts don't).
var chartTypes = map[string]bool{"line": true, "bar": true, "area": true, "pie": true}

// CreateChart persists a metrics definition on a dashboard found-or-created
// by name.
func (b *directBackend) CreateChart(ctx context.Context, in CreateChartIn) (CreateChartResult, error) {
	tenantID, userID := scope(ctx)

	name := strings.TrimSpace(in.Dashboard)
	if name == "" || len(name) > 120 {
		return CreateChartResult{}, invalidf("dashboard name must be 1-120 characters")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len(title) > 120 {
		return CreateChartResult{}, invalidf("title must be 1-120 characters")
	}
	if !chartTypes[in.ChartType] {
		return CreateChartResult{}, invalidf("unknown chart_type %q; use line, bar, area or pie", in.ChartType)
	}
	if len(in.Series) == 0 || len(in.Series) > 5 {
		return CreateChartResult{}, invalidf("series must list 1-5 value columns")
	}
	if in.XKey == "" || !slices.Contains(in.Dimensions, in.XKey) {
		return CreateChartResult{}, invalidf("x_key must be one of dimensions")
	}
	// series_key splits ONE number by a dimension's values, so it has to BE a
	// grouped dimension (else the rows don't carry it) and can't double as the
	// axis. A second value column would have nowhere to go.
	if in.SeriesKey != "" {
		if !slices.Contains(in.Dimensions, in.SeriesKey) {
			return CreateChartResult{}, invalidf("series_key %q must be one of dimensions", in.SeriesKey)
		}
		if in.SeriesKey == in.XKey {
			return CreateChartResult{}, invalidf("series_key must differ from x_key")
		}
		if len(in.Series) != 1 {
			return CreateChartResult{}, invalidf("with series_key set, series must name exactly one value column")
		}
	}

	// Dry-compile the definition with the resolved range: a bad measure,
	// dimension or filter fails NOW, where the model can correct it.
	from, to, err := metrics.RangeFromLastDays(in.LastDays, time.Now())
	if err != nil {
		return CreateChartResult{}, invalidf("%s", trimMetricsValidation(err))
	}
	if err := metrics.Validate(metrics.Request{
		Measure: in.Measure, Dimensions: in.Dimensions, Filters: in.Filters, From: from, To: to,
	}); err != nil {
		return CreateChartResult{}, invalidf("%s", trimMetricsValidation(err))
	}

	// Publishing to the org is owner/admin, same as every other write to a
	// shared dashboard. Refuse rather than quietly making it personal: a user
	// who asked for shared and got private would not find out until someone
	// else couldn't see it.
	if in.Shared && !isOwnerAdminRole(auth.RoleFromContext(ctx)) {
		return CreateChartResult{}, fmt.Errorf(
			"%w: only an owner or admin can create a shared dashboard — leave shared unset to keep this one your own", errDenied)
	}

	res := CreateChartResult{DashboardName: name}
	d, err := b.findChartDashboard(ctx, tenantID, userID, name, in.Shared)
	switch {
	case err == nil:
		// The name lookup falls back to the org's dashboard of that name, so
		// leaving `shared` unset can still land on one. Adding to it is a write
		// to what everyone sees — the same owner/admin gate as the builder's.
		if !d.personal && !isOwnerAdminRole(auth.RoleFromContext(ctx)) {
			return CreateChartResult{}, fmt.Errorf(
				"%w: %q is a shared dashboard, and only an owner or admin can add charts to it — name a dashboard of your own instead", errDenied, d.name)
		}
		res.DashboardID = d.id
		res.DashboardName = d.name // canonical casing
		res.Personal = d.personal
	case errors.Is(err, pgx.ErrNoRows):
		// A new dashboard is the caller's own unless they asked to share it:
		// a chart built in conversation is an experiment
		// until its author decides otherwise.
		owner := &userID
		if in.Shared {
			owner = nil
		}
		created, err := b.q.CreateDashboard(ctx, db.CreateDashboardParams{
			TenantID: tenantID, Name: name, CreatedBy: &userID, OwnerID: owner,
		})
		if err != nil {
			return CreateChartResult{}, fmt.Errorf("create dashboard: %w", err)
		}
		res.DashboardID = created.ID
		res.DashboardCreated = true
		res.Personal = !in.Shared
	default:
		return CreateChartResult{}, fmt.Errorf("find dashboard: %w", err)
	}

	definition, err := json.Marshal(map[string]any{
		"measure": in.Measure, "dimensions": in.Dimensions,
		"filters": in.Filters, "last_days": in.LastDays,
	})
	if err != nil {
		return CreateChartResult{}, fmt.Errorf("marshal definition: %w", err)
	}
	vizFields := map[string]any{
		"type": in.ChartType, "x_key": in.XKey, "series": in.Series,
	}
	if in.SeriesKey != "" {
		vizFields["series_key"] = in.SeriesKey
	}
	viz, err := json.Marshal(vizFields)
	if err != nil {
		return CreateChartResult{}, fmt.Errorf("marshal viz: %w", err)
	}
	chart, err := b.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: tenantID, DashboardID: res.DashboardID, Title: title,
		Definition: definition, Viz: viz, CreatedBy: &userID,
	})
	if err != nil {
		return CreateChartResult{}, fmt.Errorf("create chart: %w", err)
	}
	res.ChartID = chart.ID
	return res, nil
}

type chartDashboard struct {
	id       int64
	name     string
	personal bool
}

// findChartDashboard resolves the dashboard a chart should land on.
//
// The two lookups differ deliberately. Asking for SHARED means the org's
// dashboard of that name, so a personal one must not shadow it — otherwise
// "add it to Operations" from the Shared tab would quietly land on the caller's
// private Operations. Otherwise the caller's own wins, falling back to the
// shared one, so adding to the org's dashboard still works without spawning a
// private duplicate beside it.
func (b *directBackend) findChartDashboard(ctx context.Context, tenantID, userID int64, name string, shared bool) (chartDashboard, error) {
	if shared {
		d, err := b.q.FindSharedDashboardByName(ctx, db.FindSharedDashboardByNameParams{
			TenantID: tenantID, Name: name,
		})
		if err != nil {
			return chartDashboard{}, err
		}
		return chartDashboard{id: d.ID, name: d.Name, personal: false}, nil
	}
	d, err := b.q.FindDashboardByName(ctx, db.FindDashboardByNameParams{
		TenantID: tenantID, Name: name, UserID: &userID,
	})
	if err != nil {
		return chartDashboard{}, err
	}
	return chartDashboard{id: d.ID, name: d.Name, personal: d.OwnerID != nil}, nil
}

// SetDashboardFilters replaces a dashboard's interactive control row.
// Unlike CreateChart this never creates the dashboard:
// configuring one that doesn't exist is a typo, and silently making it would
// leave the user with an empty dashboard they didn't ask for.
func (b *directBackend) SetDashboardFilters(ctx context.Context, in SetDashboardFiltersIn) (SetDashboardFiltersResult, error) {
	tenantID, userID := scope(ctx)

	name := strings.TrimSpace(in.Dashboard)
	if name == "" || len(name) > 120 {
		return SetDashboardFiltersResult{}, invalidf("dashboard name must be 1-120 characters")
	}
	controls := in.Controls
	if controls == nil {
		controls = []string{}
	}
	cfg := dashboard.FilterConfig{Controls: controls}
	if err := cfg.Validate(); err != nil {
		return SetDashboardFiltersResult{}, invalidf("%s", err.Error())
	}

	d, err := b.q.FindDashboardByName(ctx, db.FindDashboardByNameParams{
		TenantID: tenantID, Name: name, UserID: &userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SetDashboardFiltersResult{}, fmt.Errorf("%w: no dashboard named %q — create a chart on it first", errNotFound, name)
	}
	if err != nil {
		return SetDashboardFiltersResult{}, fmt.Errorf("find dashboard: %w", err)
	}

	// Same rule as the HTTP route (dashboard.mayWrite): publishing controls to
	// the org is owner/admin, configuring your own dashboard is yours.
	if d.OwnerID == nil && !isOwnerAdminRole(auth.RoleFromContext(ctx)) {
		return SetDashboardFiltersResult{}, fmt.Errorf(
			"%w: only an owner or admin can configure a shared dashboard", errDenied)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return SetDashboardFiltersResult{}, fmt.Errorf("marshal filters: %w", err)
	}
	if _, err := b.q.UpdateDashboardFilters(ctx, db.UpdateDashboardFiltersParams{
		ID: d.ID, TenantID: tenantID, Filters: raw,
	}); err != nil {
		return SetDashboardFiltersResult{}, fmt.Errorf("update dashboard filters: %w", err)
	}
	return SetDashboardFiltersResult{DashboardID: d.ID, DashboardName: d.Name, Controls: controls}, nil
}

// SetChartFilters configures one chart's OWN control row.
//
// Same rules as the dashboard version — closed control set, mayWrite on the
// dashboard the chart sits on — differing only in what it names: a chart is
// identified by title WITHIN a dashboard, because two dashboards may each hold
// a "Volume".
func (b *directBackend) SetChartFilters(ctx context.Context, in SetChartFiltersIn) (SetChartFiltersResult, error) {
	tenantID, userID := scope(ctx)

	dashName := strings.TrimSpace(in.Dashboard)
	chartTitle := strings.TrimSpace(in.Chart)
	if dashName == "" || chartTitle == "" {
		return SetChartFiltersResult{}, invalidf("both dashboard and chart are required")
	}
	controls := in.Controls
	if controls == nil {
		controls = []string{}
	}
	cfg := dashboard.FilterConfig{Controls: controls}
	if err := cfg.Validate(); err != nil {
		return SetChartFiltersResult{}, invalidf("%s", err.Error())
	}

	d, err := b.q.FindDashboardByName(ctx, db.FindDashboardByNameParams{
		TenantID: tenantID, Name: dashName, UserID: &userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SetChartFiltersResult{}, fmt.Errorf("%w: no dashboard named %q", errNotFound, dashName)
	}
	if err != nil {
		return SetChartFiltersResult{}, fmt.Errorf("find dashboard: %w", err)
	}
	// The chart's gate is its DASHBOARD's: configuring a chart on a shared
	// dashboard changes what the whole org reads.
	if d.OwnerID == nil && !isOwnerAdminRole(auth.RoleFromContext(ctx)) {
		return SetChartFiltersResult{}, fmt.Errorf(
			"%w: only an owner or admin can configure a chart on a shared dashboard", errDenied)
	}

	c, err := b.q.FindChartByTitle(ctx, db.FindChartByTitleParams{
		TenantID: tenantID, DashboardID: d.ID, Title: chartTitle, UserID: &userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SetChartFiltersResult{}, fmt.Errorf(
			"%w: no chart titled %q on dashboard %q", errNotFound, chartTitle, d.Name)
	}
	if err != nil {
		return SetChartFiltersResult{}, fmt.Errorf("find chart: %w", err)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return SetChartFiltersResult{}, fmt.Errorf("marshal controls: %w", err)
	}
	if _, err := b.q.UpdateChartControls(ctx, db.UpdateChartControlsParams{
		ID: c.ID, TenantID: tenantID, UserID: &userID, Controls: raw,
	}); err != nil {
		return SetChartFiltersResult{}, fmt.Errorf("update chart controls: %w", err)
	}
	return SetChartFiltersResult{
		ChartID: c.ID, ChartTitle: c.Title, DashboardName: d.Name, Controls: controls,
	}, nil
}
