// Copying one chart: the destination gate, the naming,
// what travels with the copy and what deliberately does not.
package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func (f *fixture) copyChart(t *testing.T, chartID, toID, userID int64, role string, confirm bool) *copyResult {
	t.Helper()
	body := `{"dashboard_id":` + itoa(toID) + `,"confirm_override":` + map[bool]string{true: "true", false: "false"}[confirm] + `}`
	rec := f.doBodyAsRole(t, http.MethodPost, "/api/dashboards/charts/"+itoa(chartID)+"/copy", f.tenantA.ID, userID, role, body)
	out := &copyResult{code: rec.Code, raw: rec.Body.String()}
	if rec.Code == http.StatusCreated {
		var got struct {
			Data struct {
				ID    int64  `json:"id"`
				Title string `json:"title"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out.id, out.title = got.Data.ID, got.Data.Title
	}
	return out
}

type copyResult struct {
	code  int
	raw   string
	id    int64
	title string
}

// The common case: a copy onto another dashboard carries everything that makes
// it the same chart, keeps its title, and leaves the original where it was.
func TestIntegrationCopyChartToAnotherDashboard(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Mine", OwnerID: &f.userA.ID})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.q.UpdateChartControls(ctx, db.UpdateChartControlsParams{
		ID: f.chartID, TenantID: f.tenantA.ID, UserID: &f.userA.ID, Controls: []byte(`{"controls":["customer"]}`),
	}); err != nil {
		t.Fatalf("controls: %v", err)
	}

	res := f.copyChart(t, f.chartID, mine.ID, f.userA.ID, "owner", false)
	if res.code != http.StatusCreated {
		t.Fatalf("copy = %d, want 201 (%s)", res.code, res.raw)
	}
	if res.title != "QA by severity" {
		t.Errorf("title = %q; a copy on another dashboard keeps its name", res.title)
	}

	src, err := f.q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{ID: f.chartID, TenantID: f.tenantA.ID, UserID: &f.userA.ID})
	if err != nil || src.DashboardID != f.dashID {
		t.Fatalf("the original moved or vanished: %+v %v", src, err)
	}
	dup, err := f.q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{ID: res.id, TenantID: f.tenantA.ID, UserID: &f.userA.ID})
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if dup.DashboardID != mine.ID {
		t.Errorf("copy landed on dashboard %d, want %d", dup.DashboardID, mine.ID)
	}
	if string(dup.Definition) != string(src.Definition) || string(dup.Viz) != string(src.Viz) {
		t.Errorf("copy is not the same chart:\n def %s vs %s\n viz %s vs %s", dup.Definition, src.Definition, dup.Viz, src.Viz)
	}
	if !strings.Contains(string(dup.Controls), "customer") {
		t.Errorf("copy lost the chart's own filters: %s", dup.Controls)
	}
}

// Beside its original the copy needs a different name, or the dashboard shows
// two identical cards.
func TestIntegrationCopyChartOntoItsOwnDashboardRenamesIt(t *testing.T) {
	f := newFixture(t)

	res := f.copyChart(t, f.chartID, f.dashID, f.userA.ID, "owner", false)
	if res.code != http.StatusCreated {
		t.Fatalf("copy = %d, want 201 (%s)", res.code, res.raw)
	}
	if res.title != "Copy of QA by severity" {
		t.Errorf("title = %q, want %q", res.title, "Copy of QA by severity")
	}
}

// A share link belongs to the chart it was issued for. A copy inheriting it
// would hand the new chart to whoever holds the original's URL.
func TestIntegrationCopyChartDoesNotCarryTheShareLink(t *testing.T) {
	f := newFixture(t)

	if rec := f.do(t, http.MethodPost, "/api/dashboards/charts/"+itoa(f.chartID)+"/share", f.tenantA.ID); rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("share: %d %s", rec.Code, rec.Body.String())
	}
	res := f.copyChart(t, f.chartID, f.dashID, f.userA.ID, "owner", false)
	if res.code != http.StatusCreated {
		t.Fatalf("copy = %d (%s)", res.code, res.raw)
	}

	var token *string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT share_token FROM dashboard_charts WHERE id = $1`, res.id).Scan(&token); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if token != nil {
		t.Fatalf("the copy inherited a share token")
	}
}

// The gate is the DESTINATION. A manager may copy the org's chart into their
// own dashboard — reading it is enough — but not onto a shared one, which is
// publishing to everyone.
func TestIntegrationCopyChartGatesOnTheDestination(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	own, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Manager own", OwnerID: &f.otherA.ID})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if res := f.copyChart(t, f.chartID, own.ID, f.otherA.ID, "manager", false); res.code != http.StatusCreated {
		t.Fatalf("manager copying a shared chart into their own = %d, want 201 (%s)", res.code, res.raw)
	}
	if res := f.copyChart(t, f.chartID, f.dashID, f.otherA.ID, "manager", false); res.code != http.StatusForbidden {
		t.Fatalf("manager copying onto a shared dashboard = %d, want 403 (%s)", res.code, res.raw)
	}
}

// A colleague's personal dashboard is invisible, as a source and as a target:
// both are the same 404 as a dashboard that does not exist.
func TestIntegrationCopyChartCannotReachAColleaguesDashboard(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	theirs, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Theirs", OwnerID: &f.otherA.ID})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	private, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: theirs.ID, Title: "Private",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["week"],"last_days":30}`),
		Viz:        []byte(`{"type":"line","x_key":"week","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart: %v", err)
	}

	if res := f.copyChart(t, private.ID, f.dashID, f.userA.ID, "owner", false); res.code != http.StatusNotFound {
		t.Errorf("copying FROM a colleague's private dashboard = %d, want 404", res.code)
	}
	if res := f.copyChart(t, f.chartID, theirs.ID, f.userA.ID, "owner", false); res.code != http.StatusNotFound {
		t.Errorf("copying ONTO a colleague's private dashboard = %d, want 404", res.code)
	}
}

func TestIntegrationCopyChartIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	body := `{"dashboard_id":` + itoa(f.dashID) + `}`
	rec := f.doBody(t, http.MethodPost, "/api/dashboards/charts/"+itoa(f.chartID)+"/copy", f.tenantB.ID, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant copy = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// Landing under a dashboard control that takes over the chart's own fixed
// filter changes its numbers, so the copy asks first — like a move does.
func TestIntegrationCopyChartWarnsOnOverride(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	target, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Filtered", OwnerID: &f.userA.ID})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.q.UpdateDashboardFilters(ctx, db.UpdateDashboardFiltersParams{
		ID: target.ID, TenantID: f.tenantA.ID, Filters: []byte(`{"controls":["customer"]}`),
	}); err != nil {
		t.Fatalf("filters: %v", err)
	}
	pinned, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: f.dashID, Title: "Acme words",
		Definition: []byte(`{"measure":"words_processed","dimensions":["week"],"filters":{"customer":"Acme"},"last_days":30}`),
		Viz:        []byte(`{"type":"area","x_key":"week","series":["words"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart: %v", err)
	}

	res := f.copyChart(t, pinned.ID, target.ID, f.userA.ID, "owner", false)
	if res.code != http.StatusConflict || !strings.Contains(res.raw, "filter_override") {
		t.Fatalf("override copy = %d, want 409 filter_override (%s)", res.code, res.raw)
	}
	if res := f.copyChart(t, pinned.ID, target.ID, f.userA.ID, "owner", true); res.code != http.StatusCreated {
		t.Fatalf("confirmed copy = %d, want 201 (%s)", res.code, res.raw)
	}
}
