package dashboard_test

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type pinState struct {
	ChartIDs     []int64 `json:"chart_ids"`
	DashboardIDs []int64 `json:"dashboard_ids"`
}

type pinnedSummary struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ChartCount int    `json:"chart_count"`
}

type pinnedDetail struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Filters struct {
		Controls []string `json:"controls"`
	} `json:"filters"`
	Charts []struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		DashboardID   int64  `json:"dashboard_id"`
		DashboardName string `json:"dashboard_name"`
	} `json:"charts"`
}

// call runs a request as userA in tenant A and requires the status.
func (f *fixture) call(t *testing.T, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	return f.callAs(t, f.userA.ID, method, path, body, want)
}

func (f *fixture) callAs(t *testing.T, userID int64, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	rec := f.doBodyAs(t, method, path, f.tenantA.ID, userID, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec
}

func (f *fixture) pins(t *testing.T) pinState {
	t.Helper()
	s := testutil.DecodeData[pinState](t, f.call(t, "GET", "/api/dashboards/pins", "", 200))
	return s
}

func (f *fixture) pinnedList(t *testing.T) []pinnedSummary {
	t.Helper()
	l := testutil.DecodeData[[]pinnedSummary](t, f.call(t, "GET", "/api/dashboards/pinned", "", 200))
	return l
}

func (f *fixture) pinnedDetail(t *testing.T, id int64) pinnedDetail {
	t.Helper()
	d := testutil.DecodeData[pinnedDetail](t, f.call(t, "GET", "/api/dashboards/pinned/"+itoa(id), "", 200))
	return d
}

func (d pinnedDetail) chartIDs() []int64 {
	ids := make([]int64, 0, len(d.Charts))
	for _, c := range d.Charts {
		ids = append(ids, c.ID)
	}
	return ids
}

func (f *fixture) newDashboard(t *testing.T, name string, owner *int64) int64 {
	t.Helper()
	d, err := f.q.CreateDashboard(context.Background(), db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: name, OwnerID: owner})
	if err != nil {
		t.Fatalf("seed dashboard %q: %v", name, err)
	}
	return d.ID
}

// Pinning a chart puts a reference on a pinned dashboard named after the
// chart's own and carrying its filter row; the original is not touched.
func TestIntegrationPinChartReferencesIt(t *testing.T) {
	f := newFixture(t)
	f.call(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters", `{"controls":["date_range","target_language"]}`, 204)
	other := f.addChart(t, f.tenantA.ID, f.dashID, "Second")

	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204) // again: no duplicate

	s := f.pins(t)
	if !slices.Equal(s.ChartIDs, []int64{f.chartID}) || len(s.DashboardIDs) != 0 {
		t.Fatalf("pins = %+v, want only the chart (one of two charts is not the whole dashboard)", s)
	}
	list := f.pinnedList(t)
	if len(list) != 1 || list[0].Name != "Ops" || list[0].ChartCount != 1 {
		t.Fatalf("pinned list = %+v, want one \"Ops\" with 1 chart", list)
	}
	d := f.pinnedDetail(t, list[0].ID)
	if !slices.Equal(d.chartIDs(), []int64{f.chartID}) || d.Charts[0].DashboardID != f.dashID || d.Charts[0].DashboardName != "Ops" {
		t.Errorf("pinned detail = %+v, want the chart with its dashboard", d.Charts)
	}
	if !slices.Equal(d.Filters.Controls, []string{"date_range", "target_language"}) {
		t.Errorf("filters = %v, want the source's", d.Filters.Controls)
	}

	// Pinning the second chart lands on the same pinned dashboard, and now the
	// whole dashboard reads as pinned.
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(other)+"/pin", "", 204)
	if l := f.pinnedList(t); len(l) != 1 || l[0].ChartCount != 2 {
		t.Errorf("after the second pin: %+v, want one pinned dashboard with 2 charts", l)
	}
	if s := f.pins(t); !slices.Contains(s.DashboardIDs, f.dashID) {
		t.Errorf("dashboard_ids = %v, want the dashboard (all its charts pinned)", s.DashboardIDs)
	}
}

// A pinned dashboard follows its source: a chart added later shows too. Unpin
// one chart and the rest stay as they were, but later charts no longer come.
func TestIntegrationPinDashboardFollowsUntilAChartIsUnpinned(t *testing.T) {
	f := newFixture(t)
	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	later := f.addChart(t, f.tenantA.ID, f.dashID, "Added later")

	list := f.pinnedList(t)
	if len(list) != 1 {
		t.Fatalf("pinned list = %+v, want one", list)
	}
	pid := list[0].ID
	if ids := f.pinnedDetail(t, pid).chartIDs(); !slices.Equal(ids, []int64{f.chartID, later}) {
		t.Fatalf("followed charts = %v, want both, the later one included", ids)
	}
	if s := f.pins(t); !slices.Contains(s.DashboardIDs, f.dashID) || !slices.Contains(s.ChartIDs, later) {
		t.Fatalf("pins = %+v, want the dashboard and both charts", s)
	}

	f.call(t, "DELETE", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	afterUnpin := f.addChart(t, f.tenantA.ID, f.dashID, "After the unpin")
	if ids := f.pinnedDetail(t, pid).chartIDs(); !slices.Equal(ids, []int64{later}) {
		t.Errorf("after unpinning one chart = %v, want only the other (and no new ones, %d)", ids, afterUnpin)
	}
	if s := f.pins(t); slices.Contains(s.DashboardIDs, f.dashID) {
		t.Errorf("dashboard still reads as pinned after one of its charts was unpinned: %+v", s)
	}
}

// Unpinning the dashboard removes every one of its charts, wherever the person
// put them, and the emptied pinned dashboards go.
func TestIntegrationUnpinDashboardRemovesItsCharts(t *testing.T) {
	f := newFixture(t)
	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	f.call(t, "DELETE", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	if l := f.pinnedList(t); len(l) != 0 {
		t.Errorf("pinned list = %+v, want empty", l)
	}
	if s := f.pins(t); len(s.ChartIDs)+len(s.DashboardIDs) != 0 {
		t.Errorf("pins = %+v, want none", s)
	}
}

// Charts move between pinned dashboards; the one left behind stops following
// its source, and goes once it is empty. Order and names are the person's own
// and never reach the originals.
func TestIntegrationPinnedChartsMoveReorderAndRename(t *testing.T) {
	f := newFixture(t)
	second := f.addChart(t, f.tenantA.ID, f.dashID, "Second")
	salesID := f.newDashboard(t, "Sales", nil)
	sales := f.addChart(t, f.tenantA.ID, salesID, "Sales chart")
	before := f.snapshot(t)

	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(sales)+"/pin", "", 204)
	list := f.pinnedList(t)
	if len(list) != 2 {
		t.Fatalf("pinned list = %+v, want Ops and Sales", list)
	}
	ops, salesPinned := list[0].ID, list[1].ID

	f.call(t, "PUT", "/api/dashboards/pinned/"+itoa(ops)+"/chart-order", `{"chart_ids":[`+itoa(second)+`,`+itoa(f.chartID)+`]}`, 204)
	if ids := f.pinnedDetail(t, ops).chartIDs(); !slices.Equal(ids, []int64{second, f.chartID}) {
		t.Errorf("after reorder = %v, want %v", ids, []int64{second, f.chartID})
	}

	f.call(t, "PUT", "/api/dashboards/pinned/charts/"+itoa(sales)+"/dashboard", `{"pinned_dashboard_id":`+itoa(ops)+`}`, 204)
	if ids := f.pinnedDetail(t, ops).chartIDs(); !slices.Equal(ids, []int64{second, f.chartID, sales}) {
		t.Errorf("after the move = %v, want the sales chart at the end", ids)
	}
	if l := f.pinnedList(t); len(l) != 1 || l[0].ID != ops {
		t.Errorf("pinned list = %+v, want the emptied Sales one gone", l)
	}
	f.call(t, "GET", "/api/dashboards/pinned/"+itoa(salesPinned), "", 404)

	f.call(t, "PUT", "/api/dashboards/pinned/"+itoa(ops)+"/name", `{"name":"My weekly view"}`, 204)
	f.call(t, "PUT", "/api/dashboards/pinned/"+itoa(ops)+"/filters", `{"controls":["date_range"]}`, 204)
	d := f.pinnedDetail(t, ops)
	if d.Name != "My weekly view" || !slices.Equal(d.Filters.Controls, []string{"date_range"}) {
		t.Errorf("pinned dashboard = %q %v, want the new name and filters", d.Name, d.Filters.Controls)
	}
	if after := f.snapshot(t); after != before {
		t.Errorf("pinning changed the original dashboards or charts:\nbefore %s\nafter  %s", before, after)
	}
}

// A pinned view is the caller's alone: a colleague, another tenant, and another
// person's personal dashboard are all out of reach.
func TestIntegrationPinnedViewIsPersonal(t *testing.T) {
	f := newFixture(t)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	pid := f.pinnedList(t)[0].ID

	f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned/"+itoa(pid), "", 404)
	f.callAs(t, f.otherA.ID, "PUT", "/api/dashboards/pinned/"+itoa(pid)+"/name", `{"name":"x"}`, 404)
	f.callAs(t, f.otherA.ID, "DELETE", "/api/dashboards/pinned/"+itoa(pid), "", 404)
	theirs := testutil.DecodeData[[]pinnedSummary](t, f.callAs(t, f.otherA.ID, "GET", "/api/dashboards/pinned", "", 200))
	if len(theirs) != 0 {
		t.Errorf("a colleague sees %+v", theirs)
	}
	if rec := f.doBodyAs(t, "GET", "/api/dashboards/pinned/"+itoa(pid), f.tenantB.ID, f.userA.ID, ""); rec.Code != 404 {
		t.Errorf("another tenant reads it: %d", rec.Code)
	}

	// The colleague's personal dashboard cannot be pinned, nor its charts.
	privateID := f.newDashboard(t, "Private", &f.otherA.ID)
	private := f.addChart(t, f.tenantA.ID, privateID, "Private chart")
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(private)+"/pin", "", 404)
	f.call(t, "POST", "/api/dashboards/"+itoa(privateID)+"/pin", "", 404)
}

// A chart the person can no longer open leaves their view: deleted, or its
// dashboard turned into someone else's personal one.
func TestIntegrationPinnedChartsFollowVisibility(t *testing.T) {
	f := newFixture(t)
	second := f.addChart(t, f.tenantA.ID, f.dashID, "Second")
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(second)+"/pin", "", 204)
	pid := f.pinnedList(t)[0].ID

	f.call(t, "DELETE", "/api/dashboards/charts/"+itoa(second), "", 204)
	if ids := f.pinnedDetail(t, pid).chartIDs(); !slices.Equal(ids, []int64{f.chartID}) {
		t.Errorf("after deleting a chart = %v, want only %d", ids, f.chartID)
	}

	if _, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
		OwnerID: &f.otherA.ID, ID: f.dashID, TenantID: f.tenantA.ID,
	}); err != nil {
		t.Fatalf("make the dashboard a colleague's: %v", err)
	}
	if l := f.pinnedList(t); len(l) != 0 {
		t.Errorf("pinned list = %+v, want nothing once the dashboard is a colleague's", l)
	}
	if s := f.pins(t); len(s.ChartIDs) != 0 {
		t.Errorf("pins = %+v, want none", s)
	}
}

// The reset dialog warns about the people whose Pinned view holds shared
// dashboards' charts.
func TestIntegrationSharedCountReportsPinnedUsers(t *testing.T) {
	f := newFixture(t)
	count := func() int64 {
		return testutil.DecodeData[struct {
			PinnedUsers int64 `json:"pinned_users"`
		}](t, f.call(t, "GET", "/api/dashboards/shared-count", "", 200)).PinnedUsers
	}
	if n := count(); n != 0 {
		t.Fatalf("pinned_users = %d before anyone pinned", n)
	}
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.callAs(t, f.otherA.ID, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	if n := count(); n != 2 {
		t.Errorf("pinned_users = %d, want 2 (one chart pin, one dashboard pin)", n)
	}
}
