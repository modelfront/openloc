package dashboarddefaults

// The dashboards every new organisation starts with.
//
// Its own package, like mtdefaults: internal/dashboard imports internal/auth
// for the claims, so the signup paths that provision a new org cannot import
// internal/dashboard back. The chart SHAPES are therefore declared here rather
// than reused from there — a duplication the round-trip test in
// internal/dashboard pins, by decoding these into the types the renderer
// actually uses.
//
// Defined in CODE, not a table. A chart definition is a sentence in the metrics
// registry's vocabulary, so the two have to move together: stored in the
// database, a renamed measure would leave every tenant holding a chart that
// 422s at render time, discovered in production. Here, TestDefaultsCompile runs
// every default through the compiler, so the same rename fails CI.
//
// Changing these affects new organisations and anyone who presses Reset. It
// does NOT reach back into orgs that already have their own dashboards — those
// keep what they built until they ask otherwise.

import (
	"context"
	"encoding/json"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// chartDefinition / chartViz mirror the shapes internal/dashboard stores in
// dashboard_charts.definition and .viz. Same json tags, by necessity rather
// than choice — see the package comment.
type chartDefinition struct {
	Measure    string            `json:"measure"`
	Dimensions []string          `json:"dimensions,omitempty"`
	Filters    map[string]string `json:"filters,omitempty"`
	LastDays   int               `json:"last_days"`
}

type chartViz struct {
	Type      string   `json:"type"`
	XKey      string   `json:"x_key"`
	Series    []string `json:"series"`
	SeriesKey string   `json:"series_key,omitempty"`
}

type filterConfig struct {
	Controls []string `json:"controls"`
}

// DefaultChart is one card: the question, and how to draw it.
type DefaultChart struct {
	Title      string
	Definition chartDefinition
	Viz        chartViz
}

// DefaultDashboard is one page of them, with its control row.
type DefaultDashboard struct {
	Name     string
	Controls []string
	Charts   []DefaultChart
}

// defaultLastDays is the window every default chart stores. The dashboards all
// carry a date-range control, so this is only what they show before anyone
// touches it — 90 days reads as a trend without being a year of history.
const defaultLastDays = 90

// Defaults are ordered as they should appear: the overview first, because it is
// the one people open without being asked to.
var Defaults = []DefaultDashboard{
	{
		Name:     "Overview",
		Controls: []string{"date_range", "unit", "granularity"},
		// Two per row. A title names no bucket ("per week"): the granularity
		// control changes it; a pie's title says "Total".
		Charts: []DefaultChart{
			{
				// Everything that entered translation, each segment once per
				// target language: the volume the rest of the page breaks down.
				Title:      "Source volume over time",
				Definition: chartDefinition{Measure: "source_volume", Dimensions: []string{"week"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "area", XKey: "week", Series: []string{"words"}},
			},
			{
				// Vendor MT only; AI engines used as MT chart on the AI dashboard.
				Title:      "MT usage by engine",
				Definition: chartDefinition{Measure: "mt_engine_usage", Dimensions: []string{"week", "mt_engine"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "mt_engine"},
			},
			{
				Title:      "Source volume by segment type",
				Definition: chartDefinition{Measure: "source_volume", Dimensions: []string{"week", "origin"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "origin"},
			},
			{
				Title:      "Total source volume by segment type",
				Definition: chartDefinition{Measure: "source_volume", Dimensions: []string{"origin"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "pie", XKey: "origin", Series: []string{"words"}},
			},
			{
				Title:      "Source volume by target language",
				Definition: chartDefinition{Measure: "source_volume", Dimensions: []string{"week", "target_language"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "target_language"},
			},
			{
				Title:      "Total source volume by target language",
				Definition: chartDefinition{Measure: "source_volume", Dimensions: []string{"target_language"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "pie", XKey: "target_language", Series: []string{"words"}},
			},
			{
				Title:      "QA issues by check type",
				Definition: chartDefinition{Measure: "qa_issues", Dimensions: []string{"week", "check_type"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"issues"}, SeriesKey: "check_type"},
			},
		},
	},
	{
		Name: "AI",
		// ai_engine is narrower than the other controls — only the AI measures
		// declare it — which is exactly right here and would be a dead dropdown
		// on Overview.
		Controls: []string{"date_range", "unit", "granularity", "ai_engine"},
		Charts: []DefaultChart{
			{
				// What was sent to an AI engine — AI MT, and segments QE/APE
				// approved or post-edited — by engine, like MT usage on Overview.
				Title:      "AI usage by engine",
				Definition: chartDefinition{Measure: "ai_engine_usage", Dimensions: []string{"week", "ai_engine"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "ai_engine"},
			},
			{
				// Tokens are per API CALL, so they cannot be attributed to a
				// segment type — a batch covers many kinds at once. By engine is
				// the finest honest split.
				Title:      "AI token cost by engine",
				Definition: chartDefinition{Measure: "ai_token_usage", Dimensions: []string{"week", "ai_engine"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"total_tokens"}, SeriesKey: "ai_engine"},
			},
			{
				// What quality prediction actually saved: words that shipped
				// without a human. Split by where they came from, so "MT we
				// trusted" reads separately from "TM we trusted".
				Title:      "Auto-approved by segment type",
				Definition: chartDefinition{Measure: "auto_approved", Dimensions: []string{"week", "segment_type"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "segment_type"},
			},
			{
				// Accepted as-is versus fixed by APE first — the same total,
				// split by how much work the model had to do.
				Title:      "Auto-approved: QE vs APE",
				Definition: chartDefinition{Measure: "auto_approved", Dimensions: []string{"week", "predict_outcome"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "predict_outcome"},
			},
		},
	},
	{
		// The people side: what each linguist did and how much they rewrote.
		// `user` filters to one linguist.
		Name:     "Team Productivity",
		Controls: []string{"date_range", "unit", "granularity", "user"},
		Charts: []DefaultChart{
			{
				Title:      "Post-editing volume by linguist",
				Definition: chartDefinition{Measure: "words_processed", Dimensions: []string{"week", "user"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"words"}, SeriesKey: "user"},
			},
			{
				Title:      "Post-editing time by linguist",
				Definition: chartDefinition{Measure: "editing_time", Dimensions: []string{"week", "user"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"minutes"}, SeriesKey: "user"},
			},
			{
				// Average character edit distance per human edit: how much each
				// linguist rewrote what they were given.
				Title:      "Post-editing effort by linguist",
				Definition: chartDefinition{Measure: "post_edit_effort", Dimensions: []string{"week", "user"}, LastDays: defaultLastDays},
				Viz:        chartViz{Type: "bar", XKey: "week", Series: []string{"avg_edit_distance"}, SeriesKey: "user"},
			},
		},
	},
}

// InstallDefaults creates the default SHARED dashboards for a tenant.
//
// Idempotent by name, matching the other per-org provisioning helpers: an org
// that already has a shared "Overview" keeps it untouched. That is what makes
// this safe to call on every signup path without a "has this run" flag.
func InstallDefaults(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64) error {
	for _, d := range Defaults {
		if _, err := q.FindSharedDashboardByName(ctx, db.FindSharedDashboardByNameParams{
			TenantID: tenantID, Name: d.Name,
		}); err == nil {
			continue // already there; leave whatever it has become
		}
		if err := createDefault(ctx, q, tenantID, createdBy, d); err != nil {
			return err
		}
	}
	return nil
}

func createDefault(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64, d DefaultDashboard) error {
	created, err := q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: tenantID, Name: d.Name, CreatedBy: createdBy, OwnerID: nil, // shared
	})
	if err != nil {
		return fmt.Errorf("create default dashboard %q: %w", d.Name, err)
	}
	filters, err := json.Marshal(filterConfig{Controls: d.Controls})
	if err != nil {
		return fmt.Errorf("marshal filters for %q: %w", d.Name, err)
	}
	if _, err := q.UpdateDashboardFilters(ctx, db.UpdateDashboardFiltersParams{
		ID: created.ID, TenantID: tenantID, Filters: filters,
	}); err != nil {
		return fmt.Errorf("set filters for %q: %w", d.Name, err)
	}
	// In order: CreateDashboardChart positions each at MAX+1.
	for _, c := range d.Charts {
		def, err := json.Marshal(c.Definition)
		if err != nil {
			return fmt.Errorf("marshal definition for %q: %w", c.Title, err)
		}
		viz, err := json.Marshal(c.Viz)
		if err != nil {
			return fmt.Errorf("marshal viz for %q: %w", c.Title, err)
		}
		if _, err := q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
			TenantID: tenantID, DashboardID: created.ID, Title: c.Title,
			Definition: def, Viz: viz, CreatedBy: createdBy,
		}); err != nil {
			return fmt.Errorf("create default chart %q: %w", c.Title, err)
		}
	}
	return nil
}

// ResetToDefaults replaces a tenant's SHARED dashboards with the defaults.
//
// Destructive by design (the product decision): every shared dashboard is
// deleted — soft-deleted, like any delete reached from the UI — including ones
// the team built themselves, and the defaults are installed fresh. Personal
// dashboards are never touched — a reset of what the ORG sees has no business
// reaching into someone's private workspace.
//
// The deleted dashboards' share links stop working with them. The caller is
// responsible for saying so before asking for confirmation.
func ResetToDefaults(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64) error {
	if _, err := q.SoftDeleteSharedDashboards(ctx, tenantID); err != nil {
		return fmt.Errorf("clear shared dashboards: %w", err)
	}
	// Nothing shared is left, so InstallDefaults' name check always misses and
	// every default is recreated.
	return InstallDefaults(ctx, q, tenantID, createdBy)
}
