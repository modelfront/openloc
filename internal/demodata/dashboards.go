package demodata

// Sample dashboards, shared by `make seed` and the demo organization: the
// shared "Operations" dashboard and one personal dashboard. Both are healed by
// name and every chart by title, so a second call only adds what is missing.

import (
	"context"
	"encoding/json"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// Dashboard names.
const (
	DashboardOperations = "Operations"
	DashboardPersonal   = "My experiments"
)

// ChartSpec is one chart: its title, definition and viz JSON.
type ChartSpec struct{ Title, Definition, Viz string }

// OperationsCharts are the charts on the shared "Operations" dashboard.
var OperationsCharts = []ChartSpec{
	{
		"Words processed per week",
		`{"measure":"words_processed","dimensions":["week"],"last_days":90}`,
		`{"type":"area","x_key":"week","series":["words"]}`,
	},
	{
		"MT volume by engine",
		`{"measure":"mt_engine_usage","dimensions":["mt_engine"],"last_days":90}`,
		`{"type":"bar","x_key":"mt_engine","series":["segments","words"]}`,
	},
	{
		// series_key splits one number by a dimension's values: the only chart
		// here that stacks.
		"Words by origin per week",
		`{"measure":"segments_by_origin","dimensions":["week","kind"],"last_days":90}`,
		`{"type":"area","x_key":"week","series":["words"],"series_key":"kind"}`,
	},
	// The rest each use another measure, and two narrow themselves with a
	// filter of their own, so the dashboard shows what a chart can ask.
	{
		"Words approved by AI review, by language",
		`{"measure":"auto_approved","dimensions":["target_language"],"last_days":90}`,
		`{"type":"bar","x_key":"target_language","series":["words"]}`,
	},
	{
		"Post-editing effort in German, by linguist",
		`{"measure":"post_edit_effort","dimensions":["user"],"filters":{"target_language":"de"},"last_days":90}`,
		`{"type":"bar","x_key":"user","series":["avg_edit_distance"]}`,
	},
	{
		"QA issues for Nordwind Travel, by severity",
		`{"measure":"qa_issues","dimensions":["severity"],"filters":{"customer":"` + customerNordwind + `"},"last_days":90}`,
		`{"type":"pie","x_key":"severity","series":["issues"]}`,
	},
	{
		"AI tokens by model",
		`{"measure":"ai_token_usage","dimensions":["model"],"last_days":90}`,
		`{"type":"bar","x_key":"model","series":["total_tokens"]}`,
	},
}

// personalCharts are the charts on the personal dashboard.
var personalCharts = []ChartSpec{
	{
		"QA issues by severity",
		`{"measure":"qa_issues","dimensions":["severity"],"last_days":90}`,
		`{"type":"bar","x_key":"severity","series":["issues"]}`,
	},
}

// operationsControls is the control row "Operations" gets when it has none.
var operationsControls = []string{"date_range", "unit", "granularity", "target_language", "customer", "user"}

// EnsureOperationsDashboard finds or creates the shared "Operations"
// dashboard, adds any of OperationsCharts it lacks, and turns on its controls
// when they were never configured. Returns the dashboard id and how many
// charts it added.
func EnsureOperationsDashboard(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64) (int64, int, error) {
	var id int64
	if found, err := q.FindSharedDashboardByName(ctx, db.FindSharedDashboardByNameParams{TenantID: tenantID, Name: DashboardOperations}); err == nil {
		id = found.ID
	} else if !isNoRows(err) {
		return 0, 0, fmt.Errorf("find dashboard %q: %w", DashboardOperations, err)
	} else {
		created, err := q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: tenantID, Name: DashboardOperations, CreatedBy: createdBy})
		if err != nil {
			return 0, 0, fmt.Errorf("create dashboard %q: %w", DashboardOperations, err)
		}
		id = created.ID
	}
	added, err := ensureCharts(ctx, q, tenantID, id, nil, createdBy, OperationsCharts)
	if err != nil {
		return 0, 0, err
	}
	if err := ensureControls(ctx, q, tenantID, id, operationsControls); err != nil {
		return 0, 0, err
	}
	return id, added, nil
}

// EnsurePersonalDashboard finds or creates ownerID's personal dashboard and its
// chart. Returns the dashboard id.
func EnsurePersonalDashboard(ctx context.Context, q db.Querier, tenantID, ownerID int64) (int64, error) {
	var id int64
	if found, err := q.FindDashboardByName(ctx, db.FindDashboardByNameParams{TenantID: tenantID, Name: DashboardPersonal, UserID: &ownerID}); err == nil {
		id = found.ID
	} else if !isNoRows(err) {
		return 0, fmt.Errorf("find dashboard %q: %w", DashboardPersonal, err)
	} else {
		d, err := q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: tenantID, Name: DashboardPersonal, CreatedBy: &ownerID, OwnerID: &ownerID})
		if err != nil {
			return 0, fmt.Errorf("create dashboard %q: %w", DashboardPersonal, err)
		}
		id = d.ID
	}
	if _, err := ensureCharts(ctx, q, tenantID, id, &ownerID, &ownerID, personalCharts); err != nil {
		return 0, err
	}
	return id, nil
}

// ensureCharts adds the specs the dashboard lacks, by title. owner is the
// personal dashboard's owner (nil for a shared one): its charts are visible
// only to them.
func ensureCharts(ctx context.Context, q db.Querier, tenantID, dashboardID int64, owner, createdBy *int64, specs []ChartSpec) (int, error) {
	existing, err := q.ListDashboardCharts(ctx, db.ListDashboardChartsParams{DashboardID: dashboardID, TenantID: tenantID, UserID: owner})
	if err != nil {
		return 0, fmt.Errorf("list dashboard charts: %w", err)
	}
	have := map[string]bool{}
	for _, c := range existing {
		have[c.Title] = true
	}
	added := 0
	for _, c := range specs {
		if have[c.Title] {
			continue
		}
		if _, err := q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
			TenantID: tenantID, DashboardID: dashboardID, Title: c.Title,
			Definition: []byte(c.Definition), Viz: []byte(c.Viz), CreatedBy: createdBy,
		}); err != nil {
			return added, fmt.Errorf("create chart %q: %w", c.Title, err)
		}
		added++
	}
	return added, nil
}

// ensureControls sets the dashboard's control row only when it has none, so a
// deliberately cleared row stays cleared.
func ensureControls(ctx context.Context, q db.Querier, tenantID, dashboardID int64, controls []string) error {
	d, err := q.GetDashboardTenant(ctx, db.GetDashboardTenantParams{ID: dashboardID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("get dashboard: %w", err)
	}
	var current struct {
		Controls []string `json:"controls"`
	}
	if len(d.Filters) > 0 {
		if err := json.Unmarshal(d.Filters, &current); err != nil {
			return fmt.Errorf("decode dashboard filters: %w", err)
		}
	}
	if current.Controls != nil {
		return nil
	}
	filters, err := json.Marshal(map[string][]string{"controls": controls})
	if err != nil {
		return fmt.Errorf("encode dashboard filters: %w", err)
	}
	if _, err := q.UpdateDashboardFilters(ctx, db.UpdateDashboardFiltersParams{Filters: filters, ID: dashboardID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("set dashboard filters: %w", err)
	}
	return nil
}
