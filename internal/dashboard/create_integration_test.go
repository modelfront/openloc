// Creating dashboards and charts over HTTP: the builder's
// endpoints, and the gates that keep them from being a way around the ones the
// rest of the surface enforces.
package dashboard_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/metrics"
	"openloc/internal/testutil"
)

func TestIntegrationCreateDashboardIsEmptyAndVisible(t *testing.T) {
	f := newFixture(t)

	rec := f.doBody(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, `{"name":"Builder Ops"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID       int64 `json:"id"`
			Personal bool  `json:"personal"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Data.Personal {
		t.Errorf("no personal flag was sent, so it must be shared")
	}

	// Empty is the point: "New dashboard" makes a folder.
	got := f.doAs(t, http.MethodGet, "/api/dashboards/"+itoa(created.Data.ID), f.tenantA.ID, f.userA.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", got.Code)
	}
	var detail struct {
		Data struct {
			Name   string `json:"name"`
			Charts []any  `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Data.Name != "Builder Ops" {
		t.Errorf("name = %q, want %q", detail.Data.Name, "Builder Ops")
	}
	if len(detail.Data.Charts) != 0 {
		t.Errorf("a new dashboard has %d charts, want 0", len(detail.Data.Charts))
	}
}

// A shared dashboard is published to the whole org, so a manager may make one
// of their own but not one of everyone's.
func TestIntegrationCreateSharedDashboardNeedsAdmin(t *testing.T) {
	f := newFixture(t)

	rec := f.doBodyAsRole(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, f.userA.ID, "manager", `{"name":"Manager Shared"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("shared as manager: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	own := f.doBodyAsRole(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, f.userA.ID, "manager", `{"name":"Manager Own","personal":true}`)
	if own.Code != http.StatusCreated {
		t.Fatalf("personal as manager: want 201, got %d: %s", own.Code, own.Body.String())
	}
}

func TestIntegrationCreateDashboardRejectsBadNames(t *testing.T) {
	f := newFixture(t)

	for _, tc := range []struct{ name, body string }{
		{"empty", `{"name":"   "}`},
		{"overlong", `{"name":"` + strings.Repeat("x", 121) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBody(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// "Ops" is the fixture's shared dashboard; the chat resolves dashboards by
	// name, so a second one would make that ambiguous.
	dup := f.doBody(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, `{"name":"Ops"}`)
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate name: want 409, got %d: %s", dup.Code, dup.Body.String())
	}
}

func TestIntegrationCreateChartRoundTrips(t *testing.T) {
	f := newFixture(t)

	body := `{"title":"QA issues by check","measure":"qa_issues","dimensions":["week","check_type"],` +
		`"chart_type":"bar","x_key":"week","series":["issues"],"series_key":"check_type"}`
	rec := f.doBody(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts", f.tenantA.ID, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create chart: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The real proof that the definition is sound: it renders. A chart the
	// builder can save but the dashboard cannot draw is the failure this
	// endpoint exists to prevent.
	data := f.doAs(t, http.MethodGet, "/api/dashboards/charts/"+itoa(created.Data.ID)+"/data", f.tenantA.ID, f.userA.ID)
	if data.Code != http.StatusOK {
		t.Fatalf("chart data: want 200, got %d: %s", data.Code, data.Body.String())
	}
}

func TestIntegrationCreateChartRejectsIncoherentSpecs(t *testing.T) {
	f := newFixture(t)

	for _, tc := range []struct{ name, body string }{
		{"unknown measure", `{"title":"t","measure":"nope","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`},
		{"dimension the measure lacks", `{"title":"t","measure":"qa_issues","dimensions":["week","mt_engine"],"chart_type":"line","x_key":"week","series":["issues"],"series_key":"mt_engine"}`},
		{"value the measure never returns", `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["words"]}`},
		{"axis outside the groupings", `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"severity","series":["issues"]}`},
		{"split equal to the axis", `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"],"series_key":"week"}`},
		{"unknown chart type", `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"donut","x_key":"week","series":["issues"]}`},
		{"no values", `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":[]}`},
		{"empty title", `{"title":"  ","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`},
		// The pivot reads series[0] and drops the rest, so a split plus three
		// values is a chart that silently shows one of them.
		{"split with more than one value", `{"title":"t","measure":"ai_token_usage","dimensions":["week","provider"],"chart_type":"bar","x_key":"week","series":["input_tokens","output_tokens"],"series_key":"provider"}`},
		{"split by time", `{"title":"t","measure":"qa_issues","dimensions":["week","month"],"chart_type":"bar","x_key":"week","series":["issues"],"series_key":"month"}`},
		{"split on a pie", `{"title":"t","measure":"qa_issues","dimensions":["severity","check_type"],"chart_type":"pie","x_key":"severity","series":["issues"],"series_key":"check_type"}`},
		// Words and segments on one axis is a number of nothing.
		{"values with different units", `{"title":"t","measure":"words_processed","dimensions":["week"],"chart_type":"line","x_key":"week","series":["words","segments"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBody(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts", f.tenantA.ID, tc.body)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 400 or 422, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Two token columns count the same thing, so they belong on one axis — the
// unit rule must permit that, not just forbid the mismatches.
func TestIntegrationCreateChartAllowsValuesSharingAUnit(t *testing.T) {
	f := newFixture(t)

	rec := f.doBody(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts", f.tenantA.ID,
		`{"title":"Tokens","measure":"ai_token_usage","dimensions":["week"],"chart_type":"line","x_key":"week","series":["input_tokens","output_tokens"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Controls the definition cannot use are dropped rather than stored: a chart
// showing a dropdown that changes nothing invites the reader to believe it did.
func TestIntegrationCreateChartDropsUnusableControls(t *testing.T) {
	f := newFixture(t)

	// qa_issues returns one value column, so there is no unit to switch; it has
	// no mt_engine dimension either. date_range survives.
	rec := f.doBody(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts", f.tenantA.ID,
		`{"title":"QA","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"],`+
			`"controls":["date_range","unit","mt_engine","customer"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	got := f.doAs(t, http.MethodGet, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	var detail struct {
		Data struct {
			Charts []struct {
				Title    string `json:"title"`
				Controls struct {
					Controls []string `json:"controls"`
				} `json:"controls"`
			} `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var stored []string
	for _, c := range detail.Data.Charts {
		if c.Title == "QA" {
			stored = c.Controls.Controls
		}
	}
	want := []string{"date_range", "customer"}
	if !slices.Equal(stored, want) {
		t.Fatalf("controls = %v, want %v", stored, want)
	}
}

// Adding a chart is a write, so it obeys the same gate as every other write:
// a manager cannot add to a shared dashboard, and another tenant gets a 404.
func TestIntegrationCreateChartRespectsWriteGate(t *testing.T) {
	f := newFixture(t)

	rec := f.doBodyAsRole(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts",
		f.tenantA.ID, f.otherA.ID, "manager",
		`{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("manager on a shared dashboard: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// And across tenants it is a 404, like every other chart write.
	foreign := f.doBody(t, http.MethodPost, "/api/dashboards/"+itoa(f.dashID)+"/charts", f.tenantB.ID,
		`{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant: want 404, got %d: %s", foreign.Code, foreign.Body.String())
	}
}

// The catalog is what the builder offers, so a measure missing from it is a
// measure nobody can chart. Generated from the registry for exactly that
// reason — this pins that it stays generated.
func TestIntegrationMetricsCatalogCoversTheRegistry(t *testing.T) {
	f := newFixture(t)

	rec := f.do(t, http.MethodGet, "/api/dashboards/metrics-catalog", f.tenantA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Measures []struct {
				Name       string   `json:"name"`
				Definition string   `json:"definition"`
				Dimensions []string `json:"dimensions"`
				Values     []struct {
					Name string `json:"name"`
					Unit string `json:"unit"`
				} `json:"values"`
				Filters []string `json:"filters"`
			} `json:"measures"`
			Grains []string `json:"grains"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	listed := make([]string, 0, len(out.Data.Measures))
	for _, m := range out.Data.Measures {
		listed = append(listed, m.Name)
	}
	want := slices.Sorted(maps.Keys(metrics.Definitions()))
	if slices.Sort(listed); !slices.Equal(listed, want) {
		t.Fatalf("catalog measures = %v, want the registry's %v", listed, want)
	}
	if len(out.Data.Grains) != 3 {
		t.Errorf("grains = %v, want day/week/month", out.Data.Grains)
	}
	for _, m := range out.Data.Measures {
		if m.Definition == "" {
			t.Errorf("%s has no definition; the builder would offer an unexplained number", m.Name)
		}
		if len(m.Values) == 0 {
			t.Errorf("%s returns no values; nothing could be plotted", m.Name)
		}
		for _, v := range m.Values {
			if v.Unit == "" {
				t.Errorf("%s.%s has no unit; the builder could not tell what combines with what", m.Name, v.Name)
			}
		}
		// Every measure runs over a window, so date_range is always offerable.
		if !slices.Contains(m.Filters, "date_range") {
			t.Errorf("%s offers no date range filter", m.Name)
		}
		// project_id is a join key, never something to group a chart by; grains
		// travel separately, so a picker cannot list "week" twice.
		for _, d := range m.Dimensions {
			if d == "project_id" {
				t.Errorf("%s offers project_id as a grouping", m.Name)
			}
			if d == "day" || d == "week" || d == "month" {
				t.Errorf("%s lists the time grain %q among its dimensions", m.Name, d)
			}
		}
	}
}

// Editing re-states the whole chart, so the round trip has to land on the
// stored row — not just return 204.
func TestIntegrationUpdateChartRoundTrips(t *testing.T) {
	f := newFixture(t)

	body := `{"title":"QA per week","measure":"qa_issues","dimensions":["week","severity"],` +
		`"chart_type":"bar","x_key":"week","series":["issues"],"series_key":"severity","controls":["customer"]}`
	rec := f.doBody(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID), f.tenantA.ID, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("update: want 204, got %d: %s", rec.Code, rec.Body.String())
	}

	got := f.doAs(t, http.MethodGet, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	var detail struct {
		Data struct {
			Charts []struct {
				ID         int64 `json:"id"`
				Title      string
				Definition struct {
					Measure    string   `json:"measure"`
					Dimensions []string `json:"dimensions"`
				} `json:"definition"`
				Viz struct {
					Type      string   `json:"type"`
					XKey      string   `json:"x_key"`
					Series    []string `json:"series"`
					SeriesKey string   `json:"series_key"`
				} `json:"viz"`
				Controls struct {
					Controls []string `json:"controls"`
				} `json:"controls"`
			} `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range detail.Data.Charts {
		if c.ID != f.chartID {
			continue
		}
		if c.Title != "QA per week" {
			t.Errorf("title = %q", c.Title)
		}
		if c.Viz.XKey != "week" || c.Viz.SeriesKey != "severity" || c.Viz.Type != "bar" {
			t.Errorf("viz = %+v", c.Viz)
		}
		if !slices.Equal(c.Definition.Dimensions, []string{"week", "severity"}) {
			t.Errorf("dimensions = %v", c.Definition.Dimensions)
		}
		if !slices.Equal(c.Controls.Controls, []string{"customer"}) {
			t.Errorf("controls = %v", c.Controls.Controls)
		}
		// The chart renders after the edit — the point of validating it.
		data := f.doAs(t, http.MethodGet, "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID, f.userA.ID)
		if data.Code != http.StatusOK {
			t.Fatalf("chart data after edit: want 200, got %d: %s", data.Code, data.Body.String())
		}
		return
	}
	t.Fatal("the edited chart is no longer on its dashboard")
}

// A value frozen by an earlier filter removal is not something the builder can
// see or set, so an edit must not wipe it: renaming a chart would otherwise
// quietly widen what it counts.
func TestIntegrationUpdateChartKeepsFrozenFilterValues(t *testing.T) {
	f := newFixture(t)

	// Freeze severity=error by removing the control that was showing it.
	if rec := f.doBody(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID),
		f.tenantA.ID, `{"title":"QA","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"],"controls":["customer"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed update: %d: %s", rec.Code, rec.Body.String())
	}
	if rec := f.doBody(t, http.MethodPost, "/api/dashboards/charts/"+itoa(f.chartID)+"/filters/remove",
		f.tenantA.ID, `{"control":"customer","value":"Acme"}`); rec.Code != http.StatusOK {
		t.Fatalf("freeze: %d: %s", rec.Code, rec.Body.String())
	}

	// An unrelated edit — a new title.
	if rec := f.doBody(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID),
		f.tenantA.ID, `{"title":"QA renamed","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("rename update: %d: %s", rec.Code, rec.Body.String())
	}

	got := f.doAs(t, http.MethodGet, "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	var detail struct {
		Data struct {
			Charts []struct {
				ID         int64 `json:"id"`
				Definition struct {
					Filters map[string]string `json:"filters"`
				} `json:"definition"`
			} `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range detail.Data.Charts {
		if c.ID == f.chartID {
			if c.Definition.Filters["customer"] != "Acme" {
				t.Fatalf("frozen filters = %v, want customer=Acme", c.Definition.Filters)
			}
			return
		}
	}
	t.Fatal("chart not found")
}

func TestIntegrationUpdateChartRespectsWriteGate(t *testing.T) {
	f := newFixture(t)
	body := `{"title":"t","measure":"qa_issues","dimensions":["week"],"chart_type":"line","x_key":"week","series":["issues"]}`

	rec := f.doBodyAsRole(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID),
		f.tenantA.ID, f.otherA.ID, "manager", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("manager on a shared dashboard: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	foreign := f.doBody(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID), f.tenantB.ID, body)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant: want 404, got %d: %s", foreign.Code, foreign.Body.String())
	}
}

// The rules are one function, so an edit cannot save a chart that create would
// have refused.
func TestIntegrationUpdateChartRejectsIncoherentSpecs(t *testing.T) {
	f := newFixture(t)

	for _, tc := range []struct{ name, body string }{
		{"split with more than one value", `{"title":"t","measure":"ai_token_usage","dimensions":["week","provider"],"chart_type":"bar","x_key":"week","series":["input_tokens","output_tokens"],"series_key":"provider"}`},
		{"values with different units", `{"title":"t","measure":"words_processed","dimensions":["week"],"chart_type":"line","x_key":"week","series":["words","segments"]}`},
		{"dimension the measure lacks", `{"title":"t","measure":"qa_issues","dimensions":["week","mt_engine"],"chart_type":"line","x_key":"week","series":["issues"],"series_key":"mt_engine"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBody(t, http.MethodPut, "/api/dashboards/charts/"+itoa(f.chartID), f.tenantA.ID, tc.body)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 400 or 422, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// The built-in set comes out in its declared order, not alphabetically: a
// dashboard is appended to the end of its side, so "Overview" (declared first)
// sorts before "AI" even though "AI" sorts first by name.
func TestIntegrationDefaultDashboardsKeepTheirDeclaredOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fresh := testutil.SeedTenant(t, f.q, "DefaultsOrder")
	owner := testutil.SeedUserWithRole(t, f.q, fresh.ID, "defaults-order@test.com", "pass", "owner")
	if err := dashboarddefaults.InstallDefaults(ctx, f.q, fresh.ID, &owner.ID); err != nil {
		t.Fatalf("install: %v", err)
	}

	rows, err := f.q.ListDashboards(ctx, db.ListDashboardsParams{TenantID: fresh.ID, UserID: &owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Name)
	}
	want := make([]string, 0, len(dashboarddefaults.Defaults))
	for _, d := range dashboarddefaults.Defaults {
		want = append(want, d.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want the declared %v", got, want)
	}
}

// A dashboard made later goes after the ones already there, on its own side —
// not to the front, which is where position 0 would sort it.
func TestIntegrationNewDashboardGoesLastOnItsSide(t *testing.T) {
	f := newFixture(t)

	if rec := f.doBody(t, http.MethodPost, "/api/dashboards", f.tenantA.ID, `{"name":"Aardvark"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := f.q.ListDashboards(context.Background(), db.ListDashboardsParams{TenantID: f.tenantA.ID, UserID: &f.userA.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var shared []string
	for _, r := range rows {
		if r.OwnerID == nil {
			shared = append(shared, r.Name)
		}
	}
	// "Ops" existed first; "Aardvark" would sort before it by name.
	if !slices.Equal(shared, []string{"Ops", "Aardvark"}) {
		t.Fatalf("shared order = %v, want [Ops Aardvark]", shared)
	}
}
