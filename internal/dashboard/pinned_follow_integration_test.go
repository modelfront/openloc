package dashboard_test

import (
	"context"
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// A chart that is already a reference stays unpinned after its pinned
// dashboard starts following the source: unpinning it also ends the follow,
// so the source cannot bring it back.
func TestIntegrationUnpinReferencedChartOfFollowedDashboard(t *testing.T) {
	f := newFixture(t)
	second := f.addChart(t, f.tenantA.ID, f.dashID, "Second")
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	pid := f.pinnedList(t)[0].ID

	f.call(t, "DELETE", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	if ids := f.pinnedDetail(t, pid).chartIDs(); !slices.Equal(ids, []int64{second}) {
		t.Errorf("after unpinning = %v, want only %d", ids, second)
	}
	s := f.pins(t)
	if slices.Contains(s.ChartIDs, f.chartID) || slices.Contains(s.DashboardIDs, f.dashID) {
		t.Errorf("pins = %+v, want the chart and its dashboard unpinned", s)
	}
}

// Reordering a followed pinned dashboard turns its charts into references and
// keeps following; unpinning one of them afterwards still removes it.
func TestIntegrationUnpinAfterReorderingFollowedDashboard(t *testing.T) {
	f := newFixture(t)
	second := f.addChart(t, f.tenantA.ID, f.dashID, "Second")
	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	pid := f.pinnedList(t)[0].ID
	f.call(t, "PUT", "/api/dashboards/pinned/"+itoa(pid)+"/chart-order", `{"chart_ids":[`+itoa(second)+`,`+itoa(f.chartID)+`]}`, 204)

	f.call(t, "DELETE", "/api/dashboards/charts/"+itoa(second)+"/pin", "", 204)
	if ids := f.pinnedDetail(t, pid).chartIDs(); !slices.Equal(ids, []int64{f.chartID}) {
		t.Errorf("after unpinning = %v, want only %d", ids, f.chartID)
	}
}

// Moving a referenced chart off a followed pinned dashboard keeps the charts
// that were only shown by following: they stay, as references.
func TestIntegrationMoveReferencedChartKeepsFollowedNeighbours(t *testing.T) {
	f := newFixture(t)
	second := f.addChart(t, f.tenantA.ID, f.dashID, "Second")
	third := f.addChart(t, f.tenantA.ID, f.dashID, "Third")
	salesID := f.newDashboard(t, "Sales", nil)
	sales := f.addChart(t, f.tenantA.ID, salesID, "Sales chart")
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(sales)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/pin", "", 204)
	f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
	var ops, salesPinned int64
	for _, p := range f.pinnedList(t) {
		if p.Name == "Ops" {
			ops = p.ID
		} else {
			salesPinned = p.ID
		}
	}

	f.call(t, "PUT", "/api/dashboards/pinned/charts/"+itoa(f.chartID)+"/dashboard", `{"pinned_dashboard_id":`+itoa(salesPinned)+`}`, 204)
	if ids := f.pinnedDetail(t, ops).chartIDs(); !slices.Equal(ids, []int64{second, third}) {
		t.Errorf("left behind = %v, want %v", ids, []int64{second, third})
	}
	// It stopped following: a chart added to the source now stays out.
	f.addChart(t, f.tenantA.ID, f.dashID, "Later")
	if ids := f.pinnedDetail(t, ops).chartIDs(); !slices.Equal(ids, []int64{second, third}) {
		t.Errorf("after a new source chart = %v, want still %v", ids, []int64{second, third})
	}
}

// A followed pinned dashboard goes with its source: deleted, or made someone
// else's personal one, it leaves no empty card behind.
func TestIntegrationFollowedSourceGoneLeavesNoEmptyCard(t *testing.T) {
	for _, tc := range []struct {
		name string
		gone func(f *fixture) error
	}{
		{"deleted", func(f *fixture) error {
			_, err := f.q.SoftDeleteDashboard(context.Background(), db.SoftDeleteDashboardParams{
				ID: f.dashID, TenantID: f.tenantA.ID, UserID: &f.userA.ID,
			})
			return err
		}},
		{"made a colleague's", func(f *fixture) error {
			_, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
				OwnerID: &f.otherA.ID, ID: f.dashID, TenantID: f.tenantA.ID,
			})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.call(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/pin", "", 204)
			if err := tc.gone(f); err != nil {
				t.Fatalf("remove the source: %v", err)
			}
			if l := f.pinnedList(t); len(l) != 0 {
				t.Errorf("pinned list = %+v, want nothing", l)
			}
			if s := f.pins(t); len(s.ChartIDs)+len(s.DashboardIDs) != 0 {
				t.Errorf("pins = %+v, want none", s)
			}
		})
	}
}
