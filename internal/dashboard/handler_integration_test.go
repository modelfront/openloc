// Dashboard REST surface against a real DB: tenant
// isolation on every endpoint, live data through the semantic layer, and the
// stale-definition path (422 on the card, never a 500).
package dashboard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/dashboard"
	"openloc/internal/dashboarddefaults"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type fixture struct {
	q      db.Querier
	pool   *pgxpool.Pool
	router chi.Router

	tenantA, tenantB db.Tenant
	// userA owns the fixture's requests; otherA is a colleague in the SAME
	// tenant, which is the boundary personal dashboards add on top of tenancy.
	userA, otherA   db.User
	dashID, chartID int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	f := &fixture{q: q, pool: pool}
	f.tenantA = testutil.SeedTenant(t, q, "DashTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "DashTenantB")
	f.userA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "dash-a@test.com", "pass", "owner")
	f.otherA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "dash-a2@test.com", "pass", "owner")

	// One QA issue in tenant A so the qa_issues chart has a row.
	project := testutil.SeedProject(t, q, f.tenantA.ID, "Dash Alpha", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "a.txt", "txt")
	seg := testutil.SeedSegment(t, q, file.ID, "hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", "needs_review")
	if err := q.UpsertQAResult(ctx, db.UpsertQAResultParams{
		TranslationID: tr.ID, CheckType: "terminology", Severity: "error", Message: "m",
	}); err != nil {
		t.Fatalf("seed qa: %v", err)
	}

	d, err := q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: "Ops"})
	if err != nil {
		t.Fatalf("seed dashboard: %v", err)
	}
	f.dashID = d.ID
	c, err := q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: d.ID, Title: "QA by severity",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
		Viz:        []byte(`{"type":"bar","x_key":"severity","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart: %v", err)
	}
	f.chartID = c.ID

	h := dashboard.NewHandler(q, pool)
	r := chi.NewRouter()
	h.MountPinned(r, "/api/dashboards")
	r.Get("/api/dashboards", h.HandleList)
	r.Get("/api/dashboards/{dashboardID}", h.HandleGet)
	r.Get("/api/dashboards/charts/{chartID}/data", h.HandleChartData)
	r.Get("/api/dashboards/{dashboardID}/filter-options", h.HandleFilterOptions)
	r.Get("/api/dashboards/metrics-catalog", h.HandleMetricsCatalog)
	r.Post("/api/dashboards", h.HandleCreateDashboard)
	r.Post("/api/dashboards/{dashboardID}/charts", h.HandleCreateChart)
	r.Put("/api/dashboards/charts/{chartID}", h.HandleUpdateChart)
	r.Put("/api/dashboards/{dashboardID}/name", h.HandleRenameDashboard)
	r.Put("/api/dashboards/charts/{chartID}/title", h.HandleRenameChart)
	r.Put("/api/dashboards/charts/{chartID}/filters", h.HandleUpdateChartControls)
	r.Post("/api/dashboards/{dashboardID}/filters/remove", h.HandleRemoveDashboardFilter)
	r.Post("/api/dashboards/charts/{chartID}/filters/remove", h.HandleRemoveChartFilter)
	r.Put("/api/dashboards/{dashboardID}/filters", h.HandleUpdateFilters)
	r.Put("/api/dashboards/{dashboardID}/visibility", h.HandleSetVisibility)
	r.Put("/api/dashboards/charts/{chartID}/dashboard", h.HandleMoveChart)
	r.Post("/api/dashboards/charts/{chartID}/copy", h.HandleCopyChart)
	r.Put("/api/dashboards/{dashboardID}/chart-order", h.HandleReorderCharts)
	r.Put("/api/dashboards/order", h.HandleReorderDashboards)
	r.Post("/api/dashboards/reset-defaults", h.HandleResetDefaults)
	r.Get("/api/dashboards/shared-count", h.HandleSharedCount)
	r.Delete("/api/dashboards/{dashboardID}", h.HandleDeleteDashboard)
	r.Delete("/api/dashboards/charts/{chartID}", h.HandleDeleteChart)
	r.Post("/api/dashboards/{dashboardID}/duplicate", h.HandleDuplicateDashboard)
	r.Post("/api/dashboards/charts/{chartID}/share", h.HandleShareChart)
	r.Delete("/api/dashboards/charts/{chartID}/share", h.HandleRevokeChartShare)
	// Mounted without the manager gate, exactly as in api.go — the token IS the
	// authorization, so the role check must not be what protects this route.
	r.Get("/api/shared/charts/{token}", h.HandleGetSharedChart)
	r.Get("/api/shared/charts/{token}/data", h.HandleSharedChartData)
	f.router = r
	return f
}

func (f *fixture) do(t *testing.T, method, path string, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	return f.doAs(t, method, path, tenantID, f.userA.ID)
}

// doAs runs a request as a specific user — the axis personal dashboards are
// scoped on, which the tenant-only helper can't express.
func (f *fixture) doAs(t *testing.T, method, path string, tenantID, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: userID, Role: "owner"}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestIntegrationDashboardEndpointsAreTenantScoped(t *testing.T) {
	f := newFixture(t)

	// Tenant A sees its dashboard; tenant B sees an empty list and 404s on
	// A's detail, data and delete — same opaque not-found everywhere.
	if rec := f.do(t, "GET", "/api/dashboards", f.tenantA.ID); rec.Code != 200 {
		t.Fatalf("list A: %d", rec.Code)
	}
	var listB struct {
		Data []any `json:"data"`
	}
	recB := f.do(t, "GET", "/api/dashboards", f.tenantB.ID)
	if err := json.Unmarshal(recB.Body.Bytes(), &listB); err != nil || len(listB.Data) != 0 {
		t.Fatalf("tenant B list = %s", recB.Body.String())
	}
	for _, path := range []string{
		"/api/dashboards/" + itoa(f.dashID),
		"/api/dashboards/charts/" + itoa(f.chartID) + "/data",
	} {
		if rec := f.do(t, "GET", path, f.tenantB.ID); rec.Code != http.StatusNotFound {
			t.Errorf("tenant B GET %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := f.do(t, "DELETE", "/api/dashboards/charts/"+itoa(f.chartID), f.tenantB.ID); rec.Code != http.StatusNotFound {
		t.Errorf("tenant B delete chart = %d, want 404", rec.Code)
	}
	// A's chart is still there after B's delete attempt.
	if rec := f.do(t, "GET", "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID); rec.Code != 200 {
		t.Errorf("chart data after cross-tenant delete attempt = %d", rec.Code)
	}
}

func TestIntegrationDashboardChartDataRunsDefinition(t *testing.T) {
	f := newFixture(t)

	rec := f.do(t, "GET", "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID)
	if rec.Code != 200 {
		t.Fatalf("data: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Definition string           `json:"definition"`
			Rows       []map[string]any `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := env.Data
	if len(res.Rows) != 1 || res.Rows[0]["severity"] != "error" || res.Rows[0]["issues"].(float64) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if res.Definition == "" {
		t.Error("definition text missing — the card tooltip relies on it")
	}
}

// A chart whose stored measure has left the whitelist must render as a 422
// with the vocabulary message — the card shows it; the dashboard survives.
func TestIntegrationDashboardStaleDefinitionIs422(t *testing.T) {
	f := newFixture(t)

	if _, err := f.pool.Exec(context.Background(),
		`UPDATE dashboard_charts SET definition = '{"measure":"retired_measure","last_days":30}' WHERE id = $1`,
		f.chartID); err != nil {
		t.Fatalf("stale: %v", err)
	}
	rec := f.do(t, "GET", "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stale definition = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	// The dashboard detail still lists the chart (definition parses as JSON).
	got := f.readDashboard(t, f.dashID, f.userA.ID)
	if !slices.ContainsFunc(got.Charts, func(c dashboardChart) bool { return c.ID == f.chartID }) {
		t.Errorf("dashboard detail dropped the stale chart: %+v", got.Charts)
	}
}

func TestIntegrationDashboardCascadeDelete(t *testing.T) {
	f := newFixture(t)

	if rec := f.do(t, "DELETE", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/api/dashboards/charts/"+itoa(f.chartID)+"/data", f.tenantA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("chart survived dashboard delete: %d", rec.Code)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// doBody is `do` with a request body, for the filter-config PUT.
func (f *fixture) doBody(t *testing.T, method, path string, tenantID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.doBodyAs(t, method, path, tenantID, f.userA.ID, body)
}

func (f *fixture) doBodyAs(t *testing.T, method, path string, tenantID, userID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.doBodyAsRole(t, method, path, tenantID, userID, "owner", body)
}

// doBodyAsRole also varies the ROLE, which is what separates a shared
// dashboard's writes from a personal one's.
func (f *fixture) doBodyAsRole(t *testing.T, method, path string, tenantID, userID int64, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: userID, Role: role}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestIntegrationDashboardFilterConfigRoundTrips(t *testing.T) {
	f := newFixture(t)
	path := "/api/dashboards/" + itoa(f.dashID)

	if rec := f.doBody(t, "PUT", path+"/filters", f.tenantA.ID,
		`{"controls":["date_range","unit","customer"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT filters: %d %s", rec.Code, rec.Body.String())
	}

	rec := f.do(t, "GET", path, f.tenantA.ID)
	var got struct {
		Data struct {
			Filters dashboard.FilterConfig `json:"filters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	if len(got.Data.Filters.Controls) != 3 || got.Data.Filters.Controls[2] != "customer" {
		t.Errorf("controls = %v", got.Data.Filters.Controls)
	}
}

// An unknown control is rejected on write, so a render never meets a config it
// can't reason about.
func TestIntegrationDashboardFilterConfigRejectsUnknownControl(t *testing.T) {
	f := newFixture(t)
	path := "/api/dashboards/" + itoa(f.dashID) + "/filters"
	if rec := f.doBody(t, "PUT", path, f.tenantA.ID, `{"controls":["severity"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown control = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// The filter-options and filter-config endpoints are tenant-scoped like the
// rest of the group: tenant B cannot read or configure tenant A's dashboard.
func TestIntegrationDashboardFilterEndpointsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	path := "/api/dashboards/" + itoa(f.dashID)

	if rec := f.do(t, "GET", path+"/filter-options", f.tenantB.ID); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant filter-options = %d, want 404", rec.Code)
	}
	if rec := f.doBody(t, "PUT", path+"/filters", f.tenantB.ID, `{"controls":["unit"]}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant filter config PUT = %d, want 404", rec.Code)
	}
}

// The dropdowns list what the window actually contains — a language touched 200
// days ago is not offered on a 30-day view.
func TestIntegrationDashboardFilterOptionsNarrowWithRange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Two languages on the same project, one recent and one long past.
	project := testutil.SeedProject(t, f.q, f.tenantA.ID, "Dash Ranged", "en", []string{"fr", "it"})
	file := testutil.SeedFile(t, f.q, project.ID, "b.txt", "txt")
	seg := testutil.SeedSegment(t, f.q, file.ID, "bonjour", 1)
	recent := testutil.SeedTranslation(t, f.q, seg.ID, "fr", "needs_review")
	old := testutil.SeedTranslation(t, f.q, seg.ID, "it", "needs_review")
	for _, e := range []struct {
		id  int64
		age string
	}{{recent.ID, "3 days"}, {old.ID, "200 days"}} {
		if _, err := f.pool.Exec(ctx,
			`INSERT INTO translation_history (translation_id, kind, text, created_at)
			 VALUES ($1, 'mt', 'x', now() - $2::interval)`, e.id, e.age); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}

	options := func(lastDays int) []string {
		t.Helper()
		path := "/api/dashboards/" + itoa(f.dashID) +
			"/filter-options?last_days=" + strconv.Itoa(lastDays)
		rec := f.do(t, "GET", path, f.tenantA.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("filter-options: %d %s", rec.Code, rec.Body.String())
		}
		var got struct {
			Data map[string][]string `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v — %s", err, rec.Body.String())
		}
		return got.Data["target_language"]
	}

	// The control has to be enabled for the endpoint to derive it.
	if rec := f.doBody(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT filters: %d %s", rec.Code, rec.Body.String())
	}

	if wide := options(365); !slices.Contains(wide, "fr") || !slices.Contains(wide, "it") {
		t.Errorf("365-day options = %v, want both fr and it", wide)
	}
	narrow := options(30)
	if !slices.Contains(narrow, "fr") {
		t.Errorf("30-day options = %v, want fr", narrow)
	}
	if slices.Contains(narrow, "it") {
		t.Errorf("30-day options = %v, must not offer it (200 days old)", narrow)
	}
}

// --- Personal dashboards ---------------------------------

// A personal dashboard is invisible to everyone but its owner — including
// admins, and including by direct id. Same 404 as one that doesn't exist, so
// the list can't be probed.
func TestIntegrationDashboardPersonalIsInvisibleToOthers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "My scratch", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed personal dashboard: %v", err)
	}
	chart, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: mine.ID, Title: "Private chart",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
		Viz:        []byte(`{"type":"bar","x_key":"severity","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed personal chart: %v", err)
	}

	// The owner sees it in the list, and it's flagged personal.
	var list struct {
		Data []struct {
			ID       int64 `json:"id"`
			Personal bool  `json:"personal"`
		} `json:"data"`
	}
	rec := f.doAs(t, "GET", "/api/dashboards", f.tenantA.ID, f.userA.ID)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, d := range list.Data {
		if d.ID == mine.ID {
			found = true
			if !d.Personal {
				t.Error("owner's own dashboard not flagged personal")
			}
		}
	}
	if !found {
		t.Fatal("owner cannot see their own dashboard")
	}

	// A colleague in the SAME tenant sees neither the row nor anything under it.
	for _, path := range []string{
		"/api/dashboards/" + itoa(mine.ID),
		"/api/dashboards/" + itoa(mine.ID) + "/filter-options",
		"/api/dashboards/charts/" + itoa(chart.ID) + "/data",
	} {
		if rec := f.doAs(t, "GET", path, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
			t.Errorf("colleague GET %s = %d, want 404", path, rec.Code)
		}
	}
	rec = f.doAs(t, "GET", "/api/dashboards", f.tenantA.ID, f.otherA.ID)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, d := range list.Data {
		if d.ID == mine.ID {
			t.Error("a colleague's personal dashboard appears in the list")
		}
	}
	// ...and cannot delete what they cannot see.
	if rec := f.doAs(t, "DELETE", "/api/dashboards/"+itoa(mine.ID), f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("colleague delete = %d, want 404", rec.Code)
	}
}

// Publishing your own dashboard to Shared, and taking it back, is the owner's
// call; moving a SHARED one is owner/admin.
func TestIntegrationDashboardVisibilityRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Promote me", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := "/api/dashboards/" + itoa(mine.ID) + "/visibility"

	// Share it (the fixture's userA is an owner).
	if rec := f.doBodyAs(t, "PUT", path, f.tenantA.ID, f.userA.ID, `{"personal":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("share: %d %s", rec.Code, rec.Body.String())
	}
	// Now a colleague CAN see it.
	if rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(mine.ID), f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusOK {
		t.Errorf("shared dashboard invisible to colleague: %d", rec.Code)
	}
	// Take it back.
	if rec := f.doBodyAs(t, "PUT", path, f.tenantA.ID, f.userA.ID, `{"personal":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("make private: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(mine.ID), f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("dashboard still visible after being made private: %d", rec.Code)
	}
}

// Two people may each keep a dashboard of the same name; only the SHARED names
// have to be unique. Moving one into a taken name is a 409 the user resolves.
func TestIntegrationDashboardNameCollisions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, owner := range []int64{f.userA.ID, f.otherA.ID} {
		if _, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
			TenantID: f.tenantA.ID, Name: "Weekly", OwnerID: &owner,
		}); err != nil {
			t.Fatalf("two users should each be able to own a %q: %v", "Weekly", err)
		}
	}
	shared, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Weekly", OwnerID: nil,
	})
	if err != nil {
		t.Fatalf("a shared dashboard may also take the name: %v", err)
	}

	// Taking the shared one private collides with userA's own "Weekly".
	rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(shared.ID)+"/visibility",
		f.tenantA.ID, f.userA.ID, `{"personal":true}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("colliding rename = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// --- Moving charts between dashboards ----------------------------------------

func (f *fixture) moveChart(t *testing.T, chartID, toID, userID int64, confirm bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"dashboard_id":%d,"confirm_override":%t}`, toID, confirm)
	return f.doBodyAs(t, "PUT", "/api/dashboards/charts/"+itoa(chartID)+"/dashboard", f.tenantA.ID, userID, body)
}

// A chart carrying its own filter for something the destination CONTROLS will
// silently start answering a different question, so the move is a 409 until the
// caller acknowledges it. Nothing else about a destination can break a chart.
func TestIntegrationDashboardMoveChartWarnsOnOverride(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	target, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Filtered", OwnerID: &f.userA.ID,
	})
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

	rec := f.moveChart(t, pinned.ID, target.ID, f.userA.ID, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("override move = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// The message has to name the filter and the destination, or the user
	// cannot tell what they are agreeing to.
	if body := rec.Body.String(); !strings.Contains(body, "customer") || !strings.Contains(body, "Filtered") {
		t.Errorf("409 body should name the filter and dashboard: %s", body)
	}

	if rec := f.moveChart(t, pinned.ID, target.ID, f.userA.ID, true); rec.Code != http.StatusNoContent {
		t.Fatalf("confirmed move = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	moved, err := f.q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{
		ID: pinned.ID, TenantID: f.tenantA.ID, UserID: &f.userA.ID,
	})
	if err != nil || moved.DashboardID != target.ID {
		t.Fatalf("chart did not move: %+v %v", moved, err)
	}
}

// A chart whose own filters don't collide with the destination's controls moves
// without a prompt — the common case.
func TestIntegrationDashboardMoveChartWithoutOverride(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	target, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Plain", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	dashboardOf := func() int64 {
		t.Helper()
		c, err := f.q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{
			ID: f.chartID, TenantID: f.tenantA.ID, UserID: &f.userA.ID,
		})
		if err != nil {
			t.Fatalf("read chart: %v", err)
		}
		return c.DashboardID
	}
	if rec := f.moveChart(t, f.chartID, target.ID, f.userA.ID, false); rec.Code != http.StatusNoContent {
		t.Fatalf("plain move = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if got := dashboardOf(); got != target.ID {
		t.Fatalf("chart is on dashboard %d after the move, want %d", got, target.ID)
	}
	// A drop back on its own dashboard is a no-op, not an error.
	if rec := f.moveChart(t, f.chartID, target.ID, f.userA.ID, false); rec.Code != http.StatusNoContent {
		t.Errorf("same-dashboard move = %d, want 204", rec.Code)
	}
	if got := dashboardOf(); got != target.ID {
		t.Errorf("a same-dashboard move put the chart on %d, want %d", got, target.ID)
	}
}

// Both ends are checked: a colleague's personal dashboard is not a source or a
// destination, and it 404s rather than 403 so it can't be probed.
func TestIntegrationDashboardMoveChartChecksBothEnds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	theirs, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Theirs", OwnerID: &f.otherA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// userA cannot push a chart INTO a dashboard they can't see.
	if rec := f.moveChart(t, f.chartID, theirs.ID, f.userA.ID, false); rec.Code != http.StatusNotFound {
		t.Errorf("move into a colleague's dashboard = %d, want 404", rec.Code)
	}
	// ...nor pull one OUT of it.
	theirChart, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: theirs.ID, Title: "Private",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
		Viz:        []byte(`{"type":"bar","x_key":"severity","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart: %v", err)
	}
	if rec := f.moveChart(t, theirChart.ID, f.dashID, f.userA.ID, false); rec.Code != http.StatusNotFound {
		t.Errorf("move out of a colleague's dashboard = %d, want 404", rec.Code)
	}
}

// --- Chart order --------------------------------------------------------------

func TestIntegrationDashboardReorderCharts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	second, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: f.dashID, Title: "Second",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["check_type"],"last_days":30}`),
		Viz:        []byte(`{"type":"bar","x_key":"check_type","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	order := func() []int64 {
		t.Helper()
		rows, err := f.q.ListDashboardCharts(ctx, db.ListDashboardChartsParams{
			DashboardID: f.dashID, TenantID: f.tenantA.ID, UserID: &f.userA.ID,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := make([]int64, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	if got := order(); !slices.Equal(got, []int64{f.chartID, second.ID}) {
		t.Fatalf("seed order = %v", got)
	}

	body := fmt.Sprintf(`{"chart_ids":[%d,%d]}`, second.ID, f.chartID)
	if rec := f.doBody(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/chart-order", f.tenantA.ID, body); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	if got := order(); !slices.Equal(got, []int64{second.ID, f.chartID}) {
		t.Errorf("order after reorder = %v, want [%d %d]", got, second.ID, f.chartID)
	}
}

// A chart id from ANOTHER dashboard must not be dragged in by a crafted
// request: the update is scoped to this dashboard, so it simply affects nothing.
func TestIntegrationDashboardReorderIgnoresForeignCharts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	other, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Other", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	foreign, err := f.q.CreateDashboardChart(ctx, db.CreateDashboardChartParams{
		TenantID: f.tenantA.ID, DashboardID: other.ID, Title: "Elsewhere",
		Definition: []byte(`{"measure":"qa_issues","dimensions":["severity"],"last_days":30}`),
		Viz:        []byte(`{"type":"bar","x_key":"severity","series":["issues"]}`),
	})
	if err != nil {
		t.Fatalf("seed chart: %v", err)
	}

	// Send it SECOND, so an unscoped update would rewrite its position to 2 —
	// asserting on dashboard_id would be vacuous, since reorder only ever
	// touches position.
	if foreign.Position != 1 {
		t.Fatalf("foreign chart seeded at position %d, want 1", foreign.Position)
	}
	body := fmt.Sprintf(`{"chart_ids":[%d,%d]}`, f.chartID, foreign.ID)
	if rec := f.doBody(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/chart-order", f.tenantA.ID, body); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	after, err := f.q.GetDashboardChartTenant(ctx, db.GetDashboardChartTenantParams{
		ID: foreign.ID, TenantID: f.tenantA.ID, UserID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("read foreign chart: %v", err)
	}
	if after.Position != 1 || after.DashboardID != other.ID {
		t.Errorf("another dashboard's chart was touched: position %d (want 1), dashboard %d (want %d)",
			after.Position, after.DashboardID, other.ID)
	}
}

// Reordering is a write, so a non-admin cannot rearrange a shared dashboard.
func TestIntegrationDashboardReorderRespectsWriteGate(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`{"chart_ids":[%d]}`, f.chartID)
	rec := f.doBodyAsRole(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/chart-order",
		f.tenantA.ID, f.userA.ID, "project_manager", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager reorder of a shared dashboard = %d, want 403", rec.Code)
	}
}

// --- Renaming ----------------------------------------------------------------

func TestIntegrationDashboardRenameRoundTrips(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/name",
		f.tenantA.ID, f.userA.ID, `{"name":"  Operations  "}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("rename = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	// Trimmed on the way in: a name with edge whitespace is a different row to
	// the unique index but the same name to a human.
	if got := f.readDashboard(t, f.dashID, f.userA.ID); got.Name != "Operations" {
		t.Errorf("name = %q, want Operations", got.Name)
	}
}

func TestIntegrationChartRenameRoundTrips(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAs(t, "PUT", "/api/dashboards/charts/"+itoa(f.chartID)+"/title",
		f.tenantA.ID, f.userA.ID, `{"name":"Volume by customer"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retitle = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	got := f.readDashboard(t, f.dashID, f.userA.ID)
	if len(got.Charts) == 0 || got.Charts[0].Title != "Volume by customer" {
		t.Errorf("charts = %+v, want the new title", got.Charts)
	}
}

func TestIntegrationDashboardRenameRejectsEmptyAndOverlong(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ name, body string }{
		{"empty", `{"name":""}`},
		{"whitespace only", `{"name":"   "}`},
		{"over 120 runes", fmt.Sprintf(`{"name":%q}`, strings.Repeat("é", 121))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/name",
				f.tenantA.ID, f.userA.ID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("rename = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
		})
	}
	// The limit counts RUNES, not bytes: 120 accented characters is a legal
	// name that a byte-length check would reject.
	rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/name",
		f.tenantA.ID, f.userA.ID, fmt.Sprintf(`{"name":%q}`, strings.Repeat("é", 120)))
	if rec.Code != http.StatusNoContent {
		t.Errorf("120 accented characters = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
}

// Names are unique per side because create_chart resolves dashboards BY name.
func TestIntegrationDashboardRenameCollisionIs409(t *testing.T) {
	f := newFixture(t)
	if _, err := f.q.CreateDashboard(context.Background(), db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Taken",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/name",
		f.tenantA.ID, f.userA.ID, `{"name":"Taken"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("colliding rename = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// Renaming is a write, so it follows mayWrite: a shared dashboard is the org's.
func TestIntegrationRenameRespectsWriteGate(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ name, path, body string }{
		{"dashboard", "/api/dashboards/" + itoa(f.dashID) + "/name", `{"name":"Mine now"}`},
		{"chart", "/api/dashboards/charts/" + itoa(f.chartID) + "/title", `{"name":"Mine now"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBodyAsRole(t, "PUT", tc.path, f.tenantA.ID, f.userA.ID, "project_manager", tc.body)
			if rec.Code != http.StatusForbidden {
				t.Errorf("project_manager rename of a shared %s = %d, want 403", tc.name, rec.Code)
			}
		})
	}
}

// A colleague's personal dashboard is not renameable — and 404s rather than
// 403ing, so its existence stays unprobeable.
func TestIntegrationRenameOfSomeoneElsesPersonalDashboardIs404(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.q.SetDashboardOwner(ctx, db.SetDashboardOwnerParams{
		ID: f.dashID, TenantID: f.tenantA.ID, OwnerID: &f.userA.ID,
	}); err != nil {
		t.Fatalf("make personal: %v", err)
	}
	for _, tc := range []struct{ name, path string }{
		{"dashboard", "/api/dashboards/" + itoa(f.dashID) + "/name"},
		{"chart", "/api/dashboards/charts/" + itoa(f.chartID) + "/title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBodyAs(t, "PUT", tc.path, f.tenantA.ID, f.otherA.ID, `{"name":"Nope"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("colleague renaming a personal %s = %d, want 404", tc.name, rec.Code)
			}
		})
	}
}

func TestIntegrationRenameIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ name, path string }{
		{"dashboard", "/api/dashboards/" + itoa(f.dashID) + "/name"},
		{"chart", "/api/dashboards/charts/" + itoa(f.chartID) + "/title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.doBody(t, "PUT", tc.path, f.tenantB.ID, `{"name":"Stolen"}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("cross-tenant rename of %s = %d, want 404", tc.name, rec.Code)
			}
		})
	}
}

// dashboardBody is GET /dashboards/{id} — the shape a rename has to show up in.
type dashboardBody struct {
	Name   string           `json:"name"`
	Charts []dashboardChart `json:"charts"`
}

type dashboardChart struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

func (f *fixture) readDashboard(t *testing.T, id, userID int64) dashboardBody {
	t.Helper()
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(id), f.tenantA.ID, userID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET dashboard = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data dashboardBody `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	return body.Data
}

// The share flow, from the recipient's side: a colleague who was sent a chart
// permalink opens the SAME chart-data endpoint the page uses. On a shared
// dashboard that must succeed — it is the whole point of a copiable link —
// while the personal case above 404s.
func TestIntegrationSharedChartIsReadableByAColleague(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/dashboards/" + itoa(f.dashID),
		"/api/dashboards/charts/" + itoa(f.chartID) + "/data",
	} {
		if rec := f.doAs(t, "GET", path, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusOK {
			t.Errorf("colleague GET %s = %d, want 200 (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// The linguist gate is NOT testable here: this fixture mounts the handlers
// without the route group's RequireManager middleware. It is pinned against the
// real router in internal/flowtest/rbac_test.go.

// --- Share links --------------------------------------------------------------

// share issues a link for the fixture's chart and returns the token.
func (f *fixture) share(t *testing.T, userID int64) string {
	t.Helper()
	rec := f.doAs(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/share", f.tenantA.ID, userID)
	if rec.Code != http.StatusOK {
		t.Fatalf("share = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data.Token
}

// The point of the whole feature: a chart on a PERSONAL dashboard, which a
// colleague cannot browse to, opens for them once its owner sends the link.
func TestIntegrationShareTokenOpensAPersonalChart(t *testing.T) {
	f := newFixture(t)
	if _, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
		ID: f.dashID, TenantID: f.tenantA.ID, OwnerID: &f.userA.ID,
	}); err != nil {
		t.Fatalf("make personal: %v", err)
	}
	token := f.share(t, f.userA.ID)

	// Browsing is still closed to the colleague…
	if rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("colleague browsing the personal dashboard = %d, want 404", rec.Code)
	}
	// …and the link is open to them.
	for _, path := range []string{"/api/shared/charts/" + token, "/api/shared/charts/" + token + "/data"} {
		if rec := f.doAs(t, "GET", path, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusOK {
			t.Errorf("colleague GET %s = %d, want 200 (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// The deliberate role carve-out: a linguist cannot reach the analytics surface
// at all, but may open a chart someone handed them. This pins that the HANDLER
// adds no role check of its own; the route sitting outside the manager-tier
// group is pinned on the real router in internal/flowtest/rbac_test.go.
func TestIntegrationShareTokenWorksForALinguist(t *testing.T) {
	f := newFixture(t)
	token := f.share(t, f.userA.ID)
	rec := f.doBodyAsRole(t, "GET", "/api/shared/charts/"+token, f.tenantA.ID, f.otherA.ID, "linguist", "")
	if rec.Code != http.StatusOK {
		t.Errorf("linguist opening a shared chart = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// The boundary a token must NEVER cross. A link pasted into the wrong chat is
// the realistic leak, and it has to show another organisation nothing.
func TestIntegrationShareTokenIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	token := f.share(t, f.userA.ID)
	for _, path := range []string{"/api/shared/charts/" + token, "/api/shared/charts/" + token + "/data"} {
		if rec := f.do(t, "GET", path, f.tenantB.ID); rec.Code != http.StatusNotFound {
			t.Errorf("tenant B GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestIntegrationRevokedShareTokenStopsWorking(t *testing.T) {
	f := newFixture(t)
	token := f.share(t, f.userA.ID)
	if rec := f.doAs(t, "DELETE", "/api/dashboards/charts/"+itoa(f.chartID)+"/share",
		f.tenantA.ID, f.userA.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rec := f.doAs(t, "GET", "/api/shared/charts/"+token, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("revoked token = %d, want 404", rec.Code)
	}
}

// Re-sharing must not hand back the old token: "share again" is also "whatever
// I sent last time stops working".
func TestIntegrationResharingMintsANewTokenAndKillsTheOld(t *testing.T) {
	f := newFixture(t)
	first := f.share(t, f.userA.ID)
	second := f.share(t, f.userA.ID)
	if first == second {
		t.Fatal("re-sharing returned the same token; the old link stays live")
	}
	if rec := f.doAs(t, "GET", "/api/shared/charts/"+first, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("superseded token = %d, want 404", rec.Code)
	}
	if rec := f.doAs(t, "GET", "/api/shared/charts/"+second, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusOK {
		t.Errorf("new token = %d, want 200", rec.Code)
	}
}

func TestIntegrationUnknownShareTokenIs404(t *testing.T) {
	f := newFixture(t)
	if rec := f.doAs(t, "GET", "/api/shared/charts/not-a-real-token", f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusNotFound {
		t.Errorf("guessed token = %d, want 404", rec.Code)
	}
}

// Issuing a link is a WRITE: only someone who could edit the chart may hand it
// out, or any member could publish the org's dashboard to a linguist.
func TestIntegrationSharingRespectsTheWriteGate(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAsRole(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/share",
		f.tenantA.ID, f.userA.ID, "project_manager", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager sharing a chart on a SHARED dashboard = %d, want 403", rec.Code)
	}
}

// A colleague cannot mint a link for someone else's personal chart — the write
// gate 404s before the share path is reached.
func TestIntegrationCannotShareSomeoneElsesPersonalChart(t *testing.T) {
	f := newFixture(t)
	if _, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
		ID: f.dashID, TenantID: f.tenantA.ID, OwnerID: &f.userA.ID,
	}); err != nil {
		t.Fatalf("make personal: %v", err)
	}
	rec := f.doAs(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/share", f.tenantA.ID, f.otherA.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("colleague sharing a personal chart = %d, want 404", rec.Code)
	}
}

// Tokens must be unguessable — the ONLY thing standing between a link and the
// chart behind it.
func TestIntegrationShareTokensAreLongAndDistinct(t *testing.T) {
	f := newFixture(t)
	seen := map[string]bool{}
	for range 5 {
		tok := f.share(t, f.userA.ID)
		if len(tok) < 40 {
			t.Fatalf("token %q is %d chars — too short to resist guessing", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("token %q repeated", tok)
		}
		seen[tok] = true
	}
}

// --- Dashboard order ----------------------------------------------------------

// names reads the dashboard list in the order the API returns it.
func (f *fixture) names(t *testing.T, userID int64) []string {
	t.Helper()
	rec := f.doAs(t, "GET", "/api/dashboards", f.tenantA.ID, userID)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		out = append(out, d.Name)
	}
	return out
}

func TestIntegrationDashboardReorderPersists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// "Ops" exists; add two more. New dashboards append in creation order, and
	// the drag below produces a different one, so the two cannot coincide by
	// accident. (Both sort before "Ops" by name, so an alphabetical default
	// would fail here too.)
	var ids []int64
	for _, name := range []string{"Alpha", "Beta"} {
		d, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{TenantID: f.tenantA.ID, Name: name})
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		ids = append(ids, d.ID)
	}
	if got := f.names(t, f.userA.ID); !slices.Equal(got, []string{"Ops", "Alpha", "Beta"}) {
		t.Fatalf("default order = %v, want creation order", got)
	}

	body := fmt.Sprintf(`{"dashboard_ids":[%d,%d,%d]}`, f.dashID, ids[1], ids[0])
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/order", f.tenantA.ID, f.userA.ID, body); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := f.names(t, f.userA.ID); !slices.Equal(got, []string{"Ops", "Beta", "Alpha"}) {
		t.Errorf("order after drag = %v, want [Ops Beta Alpha]", got)
	}
}

// A shared dashboard's order is what the whole org sees, so it follows the same
// gate as every other write to one.
func TestIntegrationDashboardOrderRespectsWriteGate(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`{"dashboard_ids":[%d]}`, f.dashID)
	rec := f.doBodyAsRole(t, "PUT", "/api/dashboards/order", f.tenantA.ID, f.userA.ID, "project_manager", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager reordering SHARED dashboards = %d, want 403", rec.Code)
	}
}

// Ordering your own dashboards is yours to do, whatever your role.
func TestIntegrationPersonalDashboardReorderIsTheOwners(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Scratch", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := fmt.Sprintf(`{"dashboard_ids":[%d]}`, mine.ID)
	if rec := f.doBodyAsRole(t, "PUT", "/api/dashboards/order", f.tenantA.ID, f.userA.ID, "project_manager", body); rec.Code != http.StatusNoContent {
		t.Errorf("owner reordering their own = %d, want 204", rec.Code)
	}
}

// A single unwritable id fails the WHOLE request: a half-applied drag leaves an
// order nobody asked for.
func TestIntegrationDashboardReorderRejectsForeignIDsWholesale(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	theirs, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "Theirs", OwnerID: &f.otherA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := f.names(t, f.userA.ID)
	body := fmt.Sprintf(`{"dashboard_ids":[%d,%d]}`, f.dashID, theirs.ID)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/order", f.tenantA.ID, f.userA.ID, body); rec.Code != http.StatusNotFound {
		t.Errorf("order including a colleague's personal dashboard = %d, want 404", rec.Code)
	}
	if got := f.names(t, f.userA.ID); !slices.Equal(got, before) {
		t.Errorf("a refused reorder still moved things: %v -> %v", before, got)
	}
}

func TestIntegrationDashboardReorderIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`{"dashboard_ids":[%d]}`, f.dashID)
	if rec := f.doBody(t, "PUT", "/api/dashboards/order", f.tenantB.ID, body); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant reorder = %d, want 404", rec.Code)
	}
}

// --- Duplicating a dashboard ---------------------------------------------------

func (f *fixture) duplicate(t *testing.T, id, userID int64, personal bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"personal":%t}`, personal)
	return f.doBodyAs(t, "POST", "/api/dashboards/"+itoa(id)+"/duplicate", f.tenantA.ID, userID, body)
}

func TestIntegrationDuplicateCopiesChartsAndFilters(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["date_range","customer"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed filters: %d", rec.Code)
	}

	rec := f.duplicate(t, f.dashID, f.userA.ID, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			Personal bool   `json:"personal"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Data.Name != "Copy of Ops" || !created.Data.Personal {
		t.Errorf("copy = %+v, want a personal \"Copy of Ops\"", created.Data)
	}

	copied := f.readDashboard(t, created.Data.ID, f.userA.ID)
	if len(copied.Charts) != 1 || copied.Charts[0].Title != "QA by severity" {
		t.Errorf("charts = %+v, want the original's", copied.Charts)
	}
	// The controls are part of the view: a copy without them answers a
	// different question than the dashboard it came from.
	var body struct {
		Data struct {
			Filters dashboard.FilterConfig `json:"filters"`
		} `json:"data"`
	}
	detail := f.doAs(t, "GET", "/api/dashboards/"+itoa(created.Data.ID), f.tenantA.ID, f.userA.ID)
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(body.Data.Filters.Controls, []string{"date_range", "customer"}) {
		t.Errorf("filters = %v, want the original's", body.Data.Filters.Controls)
	}
}

// The copy is a brand-new dashboard nobody has shared anything from. Inheriting
// a live token would hand its contents to whoever still holds the old URL.
func TestIntegrationDuplicateDropsShareLinksButLeavesTheOriginals(t *testing.T) {
	f := newFixture(t)
	token := f.share(t, f.userA.ID)

	rec := f.duplicate(t, f.dashID, f.userA.ID, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d (%s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct{ ID int64 } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var shared int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM dashboard_charts WHERE dashboard_id = $1 AND share_token IS NOT NULL`,
		created.Data.ID).Scan(&shared); err != nil {
		t.Fatalf("count: %v", err)
	}
	if shared != 0 {
		t.Errorf("the copy carries %d share link(s); a new dashboard must have none", shared)
	}
	// …and copying must not disturb what the ORIGINAL already handed out.
	if rec := f.doAs(t, "GET", "/api/shared/charts/"+token, f.tenantA.ID, f.otherA.ID); rec.Code != http.StatusOK {
		t.Errorf("original's share link after a copy = %d, want 200", rec.Code)
	}
}

func TestIntegrationDuplicateNamesEachCopyDistinctly(t *testing.T) {
	f := newFixture(t)
	want := []string{"Copy of Ops", "Copy of Ops 2", "Copy of Ops 3"}
	for i, expect := range want {
		rec := f.duplicate(t, f.dashID, f.userA.ID, true)
		if rec.Code != http.StatusCreated {
			t.Fatalf("copy %d = %d (%s)", i+1, rec.Code, rec.Body.String())
		}
		var created struct {
			Data struct{ Name string } `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if created.Data.Name != expect {
			t.Errorf("copy %d named %q, want %q", i+1, created.Data.Name, expect)
		}
	}
}

// The gate is on the DESTINATION. Copying the org's dashboard into your own
// space is how you experiment on a shared view without touching it, so it is
// open to anyone who can see it.
func TestIntegrationDuplicateIntoYourOwnSpaceNeedsNoAdmin(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAsRole(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/duplicate",
		f.tenantA.ID, f.userA.ID, "project_manager", `{"personal":true}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("project_manager copying a shared dashboard to their own = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
}

// Copying INTO Shared is publishing, so it is owner/admin.
func TestIntegrationDuplicateIntoSharedNeedsAdmin(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAsRole(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/duplicate",
		f.tenantA.ID, f.userA.ID, "project_manager", `{"personal":false}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager copying into Shared = %d, want 403", rec.Code)
	}
}

func TestIntegrationDuplicateCannotReachAnotherTenant(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBody(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/duplicate",
		f.tenantB.ID, `{"personal":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant copy = %d, want 404", rec.Code)
	}
}

// A colleague's personal dashboard is not readable, so it is not copyable.
func TestIntegrationDuplicateCannotReachSomeoneElsesPersonal(t *testing.T) {
	f := newFixture(t)
	if _, err := f.q.SetDashboardOwner(context.Background(), db.SetDashboardOwnerParams{
		ID: f.dashID, TenantID: f.tenantA.ID, OwnerID: &f.userA.ID,
	}); err != nil {
		t.Fatalf("make personal: %v", err)
	}
	if rec := f.duplicate(t, f.dashID, f.otherA.ID, true); rec.Code != http.StatusNotFound {
		t.Errorf("copying a colleague's personal dashboard = %d, want 404", rec.Code)
	}
}

// --- Default dashboards ---------------------------------------------------------

func TestIntegrationResetInstallsTheDefaults(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "POST", "/api/dashboards/reset-defaults", f.tenantA.ID, f.userA.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("reset = %d (%s)", rec.Code, rec.Body.String())
	}
	names := f.names(t, f.userA.ID)
	for _, d := range dashboarddefaults.Defaults {
		if !slices.Contains(names, d.Name) {
			t.Errorf("default %q missing after reset; got %v", d.Name, names)
		}
	}
	// The fixture's own "Ops" is gone: this reset wipes shared and reinstalls.
	if slices.Contains(names, "Ops") {
		t.Error("a team-created shared dashboard survived a wipe-and-reinstall reset")
	}
	// The charts come with them — an empty default dashboard teaches nothing.
	first := dashboarddefaults.Defaults[0]
	for _, d := range f.listDashboards(t, f.userA.ID) {
		if d.Name == first.Name && d.ChartCount != int64(len(first.Charts)) {
			t.Errorf("%q has %d charts, want %d", d.Name, d.ChartCount, len(first.Charts))
		}
	}
}

// The blast radius the confirmation promises: shared only. A reset of what the
// ORG sees must not reach into anyone's private workspace.
func TestIntegrationResetLeavesPersonalDashboardsAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mine, err := f.q.CreateDashboard(ctx, db.CreateDashboardParams{
		TenantID: f.tenantA.ID, Name: "My scratch", OwnerID: &f.userA.ID,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if rec := f.doBodyAs(t, "POST", "/api/dashboards/reset-defaults", f.tenantA.ID, f.userA.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("reset = %d", rec.Code)
	}
	if rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(mine.ID), f.tenantA.ID, f.userA.ID); rec.Code != http.StatusOK {
		t.Errorf("personal dashboard after reset = %d, want 200 — a shared reset destroyed private work", rec.Code)
	}
}

// It deletes what the whole org sees, so it is not a project manager's call.
func TestIntegrationResetIsOwnerAdminOnly(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAsRole(t, "POST", "/api/dashboards/reset-defaults", f.tenantA.ID, f.userA.ID, "project_manager", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager reset = %d, want 403", rec.Code)
	}
}

func TestIntegrationResetIsTenantScoped(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBody(t, "POST", "/api/dashboards/reset-defaults", f.tenantB.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("tenant B reset = %d", rec.Code)
	}
	// Tenant A is untouched by tenant B resetting its own.
	if names := f.names(t, f.userA.ID); !slices.Contains(names, "Ops") {
		t.Errorf("tenant A lost %q when tenant B reset: %v", "Ops", names)
	}
}

// Reset twice must not stack duplicates — names are unique per side, so a
// second run that failed to clear first would error.
func TestIntegrationResetIsRepeatable(t *testing.T) {
	f := newFixture(t)
	for i := range 2 {
		if rec := f.doBodyAs(t, "POST", "/api/dashboards/reset-defaults", f.tenantA.ID, f.userA.ID, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("reset %d = %d (%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if got, want := len(f.names(t, f.userA.ID)), len(dashboarddefaults.Defaults); got != want {
		t.Errorf("after two resets there are %d dashboards, want %d", got, want)
	}
}

// The confirmation needs a number, not a vague warning.
func TestIntegrationSharedCountReportsWhatAResetWouldDestroy(t *testing.T) {
	f := newFixture(t)
	rec := f.doAs(t, "GET", "/api/dashboards/shared-count", f.tenantA.ID, f.userA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("count = %d", rec.Code)
	}
	var body struct {
		Data struct {
			Shared   int64 `json:"shared"`
			Defaults int64 `json:"defaults"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Shared != 1 {
		t.Errorf("shared = %d, want the fixture's one", body.Data.Shared)
	}
	if body.Data.Defaults != int64(len(dashboarddefaults.Defaults)) {
		t.Errorf("defaults = %d, want %d", body.Data.Defaults, len(dashboarddefaults.Defaults))
	}
}

// listDashboards decodes the list endpoint, which is where chart counts live.
func (f *fixture) listDashboards(t *testing.T, userID int64) []struct {
	Name       string `json:"name"`
	ChartCount int64  `json:"chart_count"`
} {
	t.Helper()
	rec := f.doAs(t, "GET", "/api/dashboards", f.tenantA.ID, userID)
	var body struct {
		Data []struct {
			Name       string `json:"name"`
			ChartCount int64  `json:"chart_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}

// --- Per-chart controls ---------------------------------------------------------

func (f *fixture) setChartControls(t *testing.T, userID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.doBodyAs(t, "PUT", "/api/dashboards/charts/"+itoa(f.chartID)+"/filters", f.tenantA.ID, userID, body)
}

func TestIntegrationChartControlsRoundTrip(t *testing.T) {
	f := newFixture(t)
	if rec := f.setChartControls(t, f.userA.ID, `{"controls":["customer","target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Charts []struct {
				Controls dashboard.FilterConfig `json:"controls"`
			} `json:"charts"`
		} `json:"data"`
	}
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data.Charts) == 0 {
		t.Fatal("no charts")
	}
	if !slices.Equal(body.Data.Charts[0].Controls.Controls, []string{"customer", "target_language"}) {
		t.Errorf("controls = %v", body.Data.Charts[0].Controls.Controls)
	}
}

// A chart with no controls configured must report an empty list, not null —
// the card iterates it.
func TestIntegrationChartControlsDefaultToEmpty(t *testing.T) {
	f := newFixture(t)
	var body struct {
		Data struct {
			Charts []struct {
				Controls dashboard.FilterConfig `json:"controls"`
			} `json:"charts"`
		} `json:"data"`
	}
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Charts[0].Controls.Controls == nil {
		t.Error("controls is null; the card would have to guard every iteration")
	}
}

// A chart may carry any control the dashboard can, including the three that
// reshape a card rather than narrow it. Allowed because a chart's controls
// render inline on the card, so a diverging window is visible.
func TestIntegrationChartControlsAllowDashboardWideOnes(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"date_range", "unit", "granularity"} {
		t.Run(name, func(t *testing.T) {
			rec := f.setChartControls(t, f.userA.ID, `{"controls":["`+name+`"]}`)
			if rec.Code != http.StatusNoContent {
				t.Errorf("%q on a chart = %d, want 204 (%s)", name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIntegrationChartControlsRejectUnknown(t *testing.T) {
	f := newFixture(t)
	if rec := f.setChartControls(t, f.userA.ID, `{"controls":["colour"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown control = %d, want 400", rec.Code)
	}
}

// Configuring a chart on a SHARED dashboard changes what the org sees.
func TestIntegrationChartControlsRespectTheWriteGate(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAsRole(t, "PUT", "/api/dashboards/charts/"+itoa(f.chartID)+"/filters",
		f.tenantA.ID, f.userA.ID, "project_manager", `{"controls":["customer"]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager = %d, want 403", rec.Code)
	}
}

func TestIntegrationChartControlsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	rec := f.doBody(t, "PUT", "/api/dashboards/charts/"+itoa(f.chartID)+"/filters",
		f.tenantB.ID, `{"controls":["customer"]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant = %d, want 404", rec.Code)
	}
}

// A chart can offer a dimension the dashboard does not, so the options endpoint
// has to cover both — or that dropdown renders with nothing in it.
func TestIntegrationFilterOptionsCoverChartOwnControls(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["date_range"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("dashboard filters: %d", rec.Code)
	}
	if rec := f.setChartControls(t, f.userA.ID, `{"controls":["customer"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("chart controls: %d", rec.Code)
	}
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID)+"/filter-options", f.tenantA.ID, f.userA.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("options = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string][]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body.Data["customer"]; !ok {
		t.Errorf("options = %v, want a customer list for the CHART's own control", body.Data)
	}
}

// --- Removing filters -----------------------------------------------------------

// chartDef reads back a chart's stored question, which is where a removed
// control's value has to land.
func (f *fixture) chartDef(t *testing.T, userID int64) (dashboard.ChartDefinition, dashboard.ChartViz) {
	t.Helper()
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, userID)
	var body struct {
		Data struct {
			Charts []struct {
				Definition dashboard.ChartDefinition `json:"definition"`
				Viz        dashboard.ChartViz        `json:"viz"`
			} `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data.Charts) == 0 {
		t.Fatal("no charts")
	}
	return body.Data.Charts[0].Definition, body.Data.Charts[0].Viz
}

// The rule the whole feature rests on: removing a control must not change what
// the chart shows. Its current value becomes the chart's permanent one.
func TestIntegrationRemoveDashboardFilterFreezesItsValue(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["date_range","target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed filters: %d", rec.Code)
	}
	// qa_issues declares target_language, so the control WAS reaching this chart.
	rec := f.doBodyAs(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"target_language","value":"de"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s)", rec.Code, rec.Body.String())
	}
	var removed struct {
		Data struct {
			Controls []string `json:"controls"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(removed.Data.Controls, []string{"date_range"}) {
		t.Errorf("controls = %v, want only date_range left", removed.Data.Controls)
	}
	def, _ := f.chartDef(t, f.userA.ID)
	if def.Filters["target_language"] != "de" {
		t.Errorf("filters = %v, want the removed control's value frozen in", def.Filters)
	}
}

// "All" narrows nothing, so there is nothing to preserve — the control just goes.
func TestIntegrationRemoveDashboardFilterWithNoSelectionFreezesNothing(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: %d", rec.Code)
	}
	before, _ := f.chartDef(t, f.userA.ID)
	if rec := f.doBodyAs(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"target_language","value":""}`); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d", rec.Code)
	}
	after, _ := f.chartDef(t, f.userA.ID)
	if len(after.Filters) != len(before.Filters) {
		t.Errorf("filters went from %v to %v; an empty selection must freeze nothing", before.Filters, after.Filters)
	}
}

// A control a measure never declared was already a no-op for that chart.
// Writing a filter now would change a card the removal should not touch.
func TestIntegrationRemoveDashboardFilterSkipsChartsItNeverReached(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["user"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: %d", rec.Code)
	}
	// The fixture's chart is qa_issues, which has no `user` dimension.
	rec := f.doBodyAs(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"user","value":"Anna"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s)", rec.Code, rec.Body.String())
	}
	def, _ := f.chartDef(t, f.userA.ID)
	if _, pinned := def.Filters["user"]; pinned {
		t.Errorf("a filter the measure never took was written anyway: %v", def.Filters)
	}
}

// Removing a chart's own control when the DASHBOARD offers the same one ties it
// back to the dashboard — nothing is frozen, because nothing is lost.
func TestIntegrationRemoveChartFilterFallsBackToTheDashboard(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed dashboard: %d", rec.Code)
	}
	if rec := f.setChartControls(t, f.userA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed chart: %d", rec.Code)
	}
	rec := f.doBodyAs(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"target_language","value":"de"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Frozen            bool `json:"frozen"`
			InheritsDashboard bool `json:"inherits_dashboard"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Data.InheritsDashboard || body.Data.Frozen {
		t.Errorf("result = %+v, want inherit without freezing", body.Data)
	}
	def, _ := f.chartDef(t, f.userA.ID)
	if _, pinned := def.Filters["target_language"]; pinned {
		t.Error("the value was frozen even though the dashboard still offers the control")
	}
}

// With no dashboard control to fall back to, the value has to be preserved.
func TestIntegrationRemoveChartFilterFreezesWhenNothingInherits(t *testing.T) {
	f := newFixture(t)
	if rec := f.setChartControls(t, f.userA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed chart: %d", rec.Code)
	}
	rec := f.doBodyAs(t, "POST", "/api/dashboards/charts/"+itoa(f.chartID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"target_language","value":"de"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d (%s)", rec.Code, rec.Body.String())
	}
	def, _ := f.chartDef(t, f.userA.ID)
	if def.Filters["target_language"] != "de" {
		t.Errorf("filters = %v, want the value frozen in", def.Filters)
	}
}

func TestIntegrationRemoveFilterRespectsTheWriteGate(t *testing.T) {
	f := newFixture(t)
	if rec := f.doBodyAs(t, "PUT", "/api/dashboards/"+itoa(f.dashID)+"/filters",
		f.tenantA.ID, f.userA.ID, `{"controls":["target_language"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("seed: %d", rec.Code)
	}
	rec := f.doBodyAsRole(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, "project_manager", `{"control":"target_language","value":"de"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("project_manager remove = %d, want 403", rec.Code)
	}
}

func TestIntegrationRemoveUnknownFilterIs404(t *testing.T) {
	f := newFixture(t)
	rec := f.doBodyAs(t, "POST", "/api/dashboards/"+itoa(f.dashID)+"/filters/remove",
		f.tenantA.ID, f.userA.ID, `{"control":"customer","value":"Acme"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("removing a control that is not there = %d, want 404", rec.Code)
	}
}

// The "add filter" menu is computed from the measures, so it cannot offer a
// control the chart would ignore.
func TestIntegrationDashboardReportsAvailableControls(t *testing.T) {
	f := newFixture(t)
	rec := f.doAs(t, "GET", "/api/dashboards/"+itoa(f.dashID), f.tenantA.ID, f.userA.ID)
	var body struct {
		Data struct {
			Available []string `json:"available_controls"`
			Charts    []struct {
				Available []string `json:"available_controls"`
			} `json:"charts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The fixture's chart is qa_issues by severity: no time dimension, one
	// value column, and no `user` dimension.
	chart := body.Data.Charts[0].Available
	if !slices.Contains(chart, "date_range") {
		t.Errorf("chart = %v, want date_range", chart)
	}
	for _, unwanted := range []string{"granularity", "unit", "user"} {
		if slices.Contains(chart, unwanted) {
			t.Errorf("chart = %v, must not offer %q for qa_issues by severity", chart, unwanted)
		}
	}
	if !slices.Contains(chart, "target_language") {
		t.Errorf("chart = %v, want target_language", chart)
	}
	// The dashboard's own menu is the union, which here is the one chart's.
	if !slices.Equal(body.Data.Available, chart) {
		t.Errorf("dashboard = %v, chart = %v", body.Data.Available, chart)
	}
}
