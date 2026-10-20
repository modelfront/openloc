package dashboard_test

import (
	"net/http/httptest"
	"slices"
	"testing"

	"openloc/internal/testutil"
)

// callIn runs a request as userA's id inside another tenant and requires the
// status.
func (f *fixture) callIn(t *testing.T, tenantID int64, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	rec := f.doBodyAs(t, method, path, tenantID, f.userA.ID, body)
	if rec.Code != want {
		t.Fatalf("%s %s in tenant %d = %d, want %d: %s", method, path, tenantID, rec.Code, want, rec.Body.String())
	}
	return rec
}

// Another tenant cannot pin tenant A's charts and dashboards. Unpinning is
// not a lookup — it only removes the caller's own references in the caller's
// tenant — so it answers 204 there and leaves tenant A's pins alone.
func TestIntegrationPinRoutesAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	// A reference row, which a delete that ignored the tenant would remove.
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	before := f.pins(t)

	f.callIn(t, f.tenantB.ID, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 404)
	f.callIn(t, f.tenantB.ID, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 404)
	f.callIn(t, f.tenantB.ID, "DELETE", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.callIn(t, f.tenantB.ID, "DELETE", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)

	theirs := testutil.DecodeData[pinState](t, f.callIn(t, f.tenantB.ID, "GET", "/api/dashboards/pins", "", 200))
	if len(theirs.ChartIDs)+len(theirs.DashboardIDs) != 0 {
		t.Errorf("tenant B pins = %+v, want none", theirs)
	}
	if after := f.pins(t); !slices.Equal(after.ChartIDs, before.ChartIDs) || !slices.Equal(after.DashboardIDs, before.DashboardIDs) {
		t.Errorf("tenant A pins = %+v, want unchanged %+v", after, before)
	}
}

// A chart moves only onto one of the caller's own pinned dashboards: a
// colleague's, or another tenant's, is not found and stays as it was.
func TestIntegrationMovePinnedChartOnlyToOwnPinnedDashboard(t *testing.T) {
	f := newFixture(t)
	salesID := f.newDashboard(t, "Sales", nil)
	sales := f.addChart(t, f.tenantA.ID, salesID, "Sales chart")
	f.callAs(t, f.otherA.ID, "POST", "/api/dashboards/charts/"+itoa(sales)+"/pin", "", 204)
	theirs := testutil.DecodeData[[]pinnedSummary](t, f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned", "", 200))
	colleagues := theirs[0].ID

	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	mine := f.pinnedList(t)[0].ID
	move := `{"pinned_dashboard_id":` + itoa(colleagues) + `}`
	f.call(t, "PUT", "/api/dashboards/pinned/charts/"+itoa(f.chartID)+"/dashboard", move, 404)
	f.callIn(t, f.tenantB.ID, "PUT", "/api/dashboards/pinned/charts/"+itoa(f.chartID)+"/dashboard", `{"pinned_dashboard_id":`+itoa(mine)+`}`, 404)

	d := testutil.DecodeData[pinnedDetail](t, f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned/"+itoa(colleagues), "", 200))
	if !slices.Equal(d.chartIDs(), []int64{sales}) {
		t.Errorf("colleague's pinned dashboard = %v, want only %d", d.chartIDs(), sales)
	}
	if ids := f.pinnedDetail(t, mine).chartIDs(); !slices.Equal(ids, []int64{f.chartID}) {
		t.Errorf("own pinned dashboard = %v, want only %d", ids, f.chartID)
	}
}

// The order of pinned dashboards is the caller's own: a colleague's id in the
// list changes nothing for the colleague.
func TestIntegrationReorderPinnedDashboards(t *testing.T) {
	f := newFixture(t)
	salesID := f.newDashboard(t, "Sales", nil)
	sales := f.addChart(t, f.tenantA.ID, salesID, "Sales chart")
	qaID := f.newDashboard(t, "QA", nil)
	qa := f.addChart(t, f.tenantA.ID, qaID, "QA chart")
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(sales)+"/pin", "", 204)
	f.callAs(t, f.otherA.ID, "POST", "/api/dashboards/charts/"+itoa(sales)+"/pin", "", 204)
	f.callAs(t, f.otherA.ID, "POST", "/api/dashboards/charts/"+itoa(qa)+"/pin", "", 204)
	colleagueOrder := func() []string {
		l := testutil.DecodeData[[]pinnedSummary](t, f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned", "", 200))
		names := make([]string, 0, len(l))
		for _, p := range l {
			names = append(names, p.Name)
		}
		return names
	}
	beforeColleague := colleagueOrder()
	theirs := testutil.DecodeData[[]pinnedSummary](t, f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned", "", 200))

	l := f.pinnedList(t)
	ops, salesPinned := l[0].ID, l[1].ID
	body := `{"pinned_ids":[` + itoa(theirs[1].ID) + `,` + itoa(salesPinned) + `,` + itoa(ops) + `,` + itoa(theirs[0].ID) + `]}`
	f.call(t, "PUT", "/api/dashboards/pinned/order", body, 204)

	if l := f.pinnedList(t); len(l) != 2 || l[0].ID != salesPinned || l[1].ID != ops {
		t.Errorf("own order = %+v, want Sales then Ops", l)
	}
	if got := colleagueOrder(); !slices.Equal(got, beforeColleague) {
		t.Errorf("colleague's order = %v, want unchanged %v", got, beforeColleague)
	}
}

// Filter options of a pinned dashboard are the caller's alone.
func TestIntegrationPinnedFilterOptionsAreOwnOnly(t *testing.T) {
	f := newFixture(t)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	pid := f.pinnedList(t)[0].ID
	f.call(t, "PUT", "/api/dashboards/pinned/"+itoa(pid)+"/filters", `{"controls":["target_language"]}`, 204)

	path := "/api/dashboards/pinned/" + itoa(pid) + "/filter-options?last_days=30"
	opts := testutil.DecodeData[map[string][]string](t, f.call(t, "GET", path, "", 200))
	if _, ok := opts["target_language"]; !ok {
		t.Errorf("options = %v, want a target_language list", opts)
	}
	f.callAs(t, f.otherA.ID, "GET", path, "", 404)
	f.callIn(t, f.tenantB.ID, "GET", path, "", 404)
}
