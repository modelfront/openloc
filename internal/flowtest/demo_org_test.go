package flowtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/demoorg"
	"openloc/internal/testutil"
)

// demoFixture is a customer org with an owner, plus the demo organization with
// its guide (the owner of the demo tenant, as internal/demoorg builds it).
type demoFixture struct {
	router   chi.Router
	pool     *pgxpool.Pool
	q        db.Querier
	customer db.Tenant
	owner    db.User
	demo     db.Tenant
	guide    db.User
}

func newDemoFixture(t *testing.T) *demoFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &demoFixture{router: testutil.NewTestRouter(pool), pool: pool, q: q}
	f.customer = testutil.SeedTenant(t, q, "Customer Co")
	f.owner = testutil.SeedUserWithRole(t, q, f.customer.ID, "owner@test.com", "pass", "owner")
	f.demo = testutil.SeedTenant(t, q, "OpenLoc Demo")
	if _, err := pool.Exec(context.Background(), `UPDATE tenants SET kind = 'demo' WHERE id = $1`, f.demo.ID); err != nil {
		t.Fatal(err)
	}
	f.guide = testutil.SeedUserWithRole(t, q, f.demo.ID, "guide@demo.example.com", "pass", "owner")
	return f
}

func (f *demoFixture) enter(t *testing.T) string {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, http.MethodPost, "/api/auth/demo/enter", nil, f.owner.ID, f.customer.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter demo: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct{ Token string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.Token == "" {
		t.Fatalf("enter demo body %s: %v", rec.Body.String(), err)
	}
	return env.Data.Token
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Error.Code
}

// Entering acts as the guide on the visitor's behalf; leaving returns to the
// visitor's own org with their own role.
func TestIntegrationDemoOrgEnterAndLeave(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	claims, err := auth.ValidateToken(token, testutil.TestJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !claims.Demo || claims.TenantID != f.demo.ID || claims.UserID != f.guide.ID ||
		claims.RealUserID != f.owner.ID || claims.RealTenantID != f.customer.ID || claims.Role != auth.RoleOwner {
		t.Fatalf("demo claims = %+v", claims)
	}

	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/auth/me", nil, token)
	var me struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Data["demo"] != true || me.Data["demo_return_org_name"] != "Customer Co" || me.Data["email"] != "guide@demo.example.com" {
		t.Errorf("/me in the demo = %v", me.Data)
	}

	// Reads work inside the demo.
	if rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/projects", nil, token); rec.Code != http.StatusOK {
		t.Errorf("GET /api/projects in the demo = %d %s", rec.Code, rec.Body.String())
	}
	// A second enter from inside the demo is refused.
	if rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/api/auth/demo/enter", nil, token); rec.Code != http.StatusForbidden || errorCode(t, rec) != "demo_read_only" {
		t.Errorf("enter from the demo = %d %s", rec.Code, rec.Body.String())
	}

	rec = testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/api/auth/demo/leave", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body.String())
	}
	var left struct {
		Data struct {
			Token             string `json:"token"`
			NeedsOrgSelection bool   `json:"needs_org_selection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &left); err != nil {
		t.Fatal(err)
	}
	back, err := auth.ValidateToken(left.Data.Token, testutil.TestJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if back.Demo || back.UserID != f.owner.ID || back.TenantID != f.customer.ID || back.Role != "owner" || left.Data.NeedsOrgSelection {
		t.Errorf("token after leaving = %+v (needs_org_selection=%v)", back, left.Data.NeedsOrgSelection)
	}
}

// A visitor removed from their org meanwhile goes to the org picker, not back.
func TestIntegrationDemoOrgLeaveWithoutMembership(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	if err := f.q.DeleteMembership(context.Background(), db.DeleteMembershipParams{UserID: f.owner.ID, TenantID: f.customer.ID}); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/api/auth/demo/leave", nil, token)
	var left struct {
		Data struct {
			Token             string `json:"token"`
			NeedsOrgSelection bool   `json:"needs_org_selection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &left); err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateToken(left.Data.Token, testutil.TestJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !left.Data.NeedsOrgSelection || claims.Scope != auth.ScopeIdentity || claims.UserID != f.owner.ID {
		t.Errorf("leave without membership: %+v needs=%v", claims, left.Data.NeedsOrgSelection)
	}
}

// Visitors never become members and never show in the demo org's member list.
func TestIntegrationDemoOrgVisitorIsNotAMember(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/members", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("members: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "owner@test.com") {
		t.Error("the visitor is listed as a member of the demo org")
	}
	if _, err := f.q.GetMembership(context.Background(), db.GetMembershipParams{UserID: f.owner.ID, TenantID: f.demo.ID}); err == nil {
		t.Error("entering the demo created a membership")
	}
}

// The prompt preview is a POST that only reads: the prompts tour shows its
// context body and output schema in the demo.
func TestIntegrationDemoOrgPreviewsPrompts(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/api/ai/prompts/preview",
		map[string]any{"out_status": true, "out_text": true, "out_comments": true, "segment_context": []string{"tm_match", "terms"}}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("prompt preview in the demo = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			OutputSchema map[string]any `json:"output_schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.OutputSchema) == 0 {
		t.Errorf("preview has no output schema: %s", rec.Body.String())
	}
}

// pathParam fills chi route patterns with a numeric id.
var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// Every route that is not a read is refused in the demo org, whatever it is;
// a route added later is covered without touching this test. The allowed
// POSTs are the ones that only read.
func TestIntegrationDemoOrgIsReadOnly(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	allowed := map[string]bool{
		"POST /api/chat/speech":             true,
		"POST /api/spellcheck":              true,
		"POST /api/jobs/{jobID}/spellcheck": true,
		"POST /api/auth/demo/leave":         true,
		"POST /api/ai/prompts/preview":      true,
	}
	checked := 0
	err := chi.Walk(f.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			return nil
		}
		if allowed[method+" "+route] {
			return nil
		}
		path := strings.TrimSuffix(pathParam.ReplaceAllString(route, "1"), "/*")
		// Public routes (sign-in, OAuth, invites) never see a tenant; they
		// answer an anonymous caller without a 401 and are skipped.
		anon := httptest.NewRequest(method, path, strings.NewReader("{}"))
		anonRec := httptest.NewRecorder()
		f.router.ServeHTTP(anonRec, anon)
		if anonRec.Code != http.StatusUnauthorized {
			return nil
		}
		rec := testutil.DoRequestWithRawToken(t, f.router, method, path, map[string]any{}, token)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "demo_read_only" {
			t.Errorf("%s %s in the demo org = %d %s, want 403 demo_read_only", method, route, rec.Code, rec.Body.String())
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("only %d write routes checked; the walk did not see the router", checked)
	}

	// The MCP endpoint is refused outright, reads included.
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		rec := testutil.DoRequestWithRawToken(t, f.router, method, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, token)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "demo_read_only" {
			t.Errorf("%s /mcp in the demo = %d %s", method, rec.Code, rec.Body.String())
		}
	}

	// The same writes still work for a customer org.
	rec := testutil.DoRequest(t, f.router, http.MethodPost, "/api/tms", map[string]any{"name": "Real TM", "source_language": "en", "target_languages": []string{"de"}}, f.owner.ID, f.customer.ID)
	if rec.Code >= 400 {
		t.Errorf("customer write = %d %s", rec.Code, rec.Body.String())
	}
}

// A demo token acts as the guide, so on the identity routes it may only read
// the profile and leave: renaming the guide or creating an org is refused.
func TestIntegrationDemoOrgIdentityRoutesAreLimited(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPatch, "/api/auth/me"},
		{http.MethodGet, "/api/auth/orgs"},
		{http.MethodPost, "/api/auth/orgs"},
		{http.MethodPost, "/api/auth/orgs/1/select"},
		{http.MethodGet, "/api/platform/orgs"},
	} {
		rec := testutil.DoRequestWithRawToken(t, f.router, c.method, c.path, map[string]any{"name": "Renamed"}, token)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "demo_read_only" {
			t.Errorf("%s %s with a demo token = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	guide, err := f.q.GetUser(context.Background(), f.guide.ID)
	if err != nil {
		t.Fatal(err)
	}
	if guide.Name == "Renamed" {
		t.Error("a visitor renamed the demo guide")
	}
}

// Lazy defaults on a GET are skipped in the demo org, which ships complete.
func TestIntegrationDemoOrgGetWritesNothing(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	if rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/ai/prompts", nil, token); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/ai/prompts: %d %s", rec.Code, rec.Body.String())
	}
	prompts, err := f.q.ListAIPrompts(context.Background(), f.demo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 0 {
		t.Errorf("a GET in the demo org created %d prompts", len(prompts))
	}
}

// The demo org is not an operator-managed org.
func TestIntegrationDemoOrgHiddenFromPlatformList(t *testing.T) {
	f := newDemoFixture(t)
	rows, err := f.q.ListTenantsForPlatform(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.TenantID == f.demo.ID {
			t.Error("the demo org is in the platform org list")
		}
	}
}

// Without a demo org, entering explains itself instead of failing obscurely.
func TestIntegrationDemoOrgUnavailable(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Solo")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "solo@test.com", "pass", "owner")
	rec := testutil.DoRequest(t, router, http.MethodPost, "/api/auth/demo/enter", nil, user.ID, tenant.ID)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "demo_unavailable" {
		t.Errorf("enter without a demo org = %d %s", rec.Code, rec.Body.String())
	}
}

// The demo organization runs nothing and holds no keys, so its pages do not
// warn about missing keys; a customer org with the same keyless engine does.
// The built-in prompts are there, as in any organization.
func TestIntegrationDemoOrgPagesLookComplete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()
	if _, _, err := demoorg.Ensure(ctx, pool, time.Now()); err != nil {
		t.Fatalf("build the demo org: %v", err)
	}
	customer := testutil.SeedTenant(t, q, "Customer Co")
	owner := testutil.SeedUserWithRole(t, q, customer.ID, "owner@test.com", "pass", "owner")
	rec := testutil.DoRequest(t, router, http.MethodPost, "/api/auth/demo/enter", nil, owner.ID, customer.ID)
	var env struct {
		Data struct{ Token string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.Token == "" {
		t.Fatalf("enter demo: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/ai/engines", "/api/ai/routers", "/api/mt-engines", "/api/mt-routers"} {
		rec := testutil.DoRequestWithRawToken(t, router, http.MethodGet, path, nil, env.Data.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s in the demo: %d %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"no_key"`) {
			t.Errorf("GET %s in the demo warns about a missing key: %s", path, rec.Body.String())
		}
	}
	rec = testutil.DoRequestWithRawToken(t, router, http.MethodGet, "/api/ai/prompts", nil, env.Data.Token)
	if !strings.Contains(rec.Body.String(), `"is_default":true`) {
		t.Errorf("the demo org has no built-in prompts: %s", rec.Body.String())
	}

	// The same keyless engine in a customer org is a real problem to show.
	demodataEngine(t, pool, customer.ID)
	rec = testutil.DoRequest(t, router, http.MethodGet, "/api/ai/engines", nil, owner.ID, customer.ID)
	if !strings.Contains(rec.Body.String(), `"no_key"`) {
		t.Errorf("a customer org's keyless engine is not flagged: %s", rec.Body.String())
	}
}

// Every chart on the demo's dashboards has data, whatever its measure or its
// own filter: the dashboards tour shows them as what a chart can answer.
func TestIntegrationDemoOrgChartsAllHaveData(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	if _, _, err := demoorg.Ensure(context.Background(), pool, time.Now()); err != nil {
		t.Fatalf("build the demo org: %v", err)
	}
	customer := testutil.SeedTenant(t, q, "Customer Co")
	owner := testutil.SeedUserWithRole(t, q, customer.ID, "owner@test.com", "pass", "owner")
	rec := testutil.DoRequest(t, router, http.MethodPost, "/api/auth/demo/enter", nil, owner.ID, customer.ID)
	var env struct {
		Data struct{ Token string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.Token == "" {
		t.Fatalf("enter demo: %d %s", rec.Code, rec.Body.String())
	}
	get := func(path string, out any) {
		t.Helper()
		rec := testutil.DoRequestWithRawToken(t, router, http.MethodGet, path, nil, env.Data.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var list struct {
		Data []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	get("/api/dashboards", &list)
	charts := 0
	for _, d := range list.Data {
		var detail struct {
			Data struct {
				Charts []struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				} `json:"charts"`
			} `json:"data"`
		}
		get(fmt.Sprintf("/api/dashboards/%d", d.ID), &detail)
		for _, c := range detail.Data.Charts {
			var data struct {
				Data struct {
					Rows []map[string]any `json:"rows"`
				} `json:"data"`
			}
			get(fmt.Sprintf("/api/dashboards/charts/%d/data", c.ID), &data)
			if len(data.Data.Rows) == 0 {
				t.Errorf("%s / %s has no data in the demo", d.Name, c.Title)
			}
			charts++
		}
	}
	if charts < len(demodata.OperationsCharts) {
		t.Errorf("the demo dashboards show %d charts, want at least the %d of Operations", charts, len(demodata.OperationsCharts))
	}
}

// demodataEngine adds an enabled AI engine with one keyless row.
func demodataEngine(t *testing.T, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	ctx := context.Background()
	var engineID, promptID int64
	if err := pool.QueryRow(ctx, `INSERT INTO ai_prompts (tenant_id, name, prompt_text) VALUES ($1, 'P', 'x') RETURNING id`, tenantID).Scan(&promptID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ai_engines (tenant_id, name, purpose, enabled) VALUES ($1, 'Keyless', 'qe_ape', true) RETURNING id`, tenantID).Scan(&engineID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_engine_rows (engine_id, source_lang, target_langs, system_prompt_id, model, settings, sort_order)
		VALUES ($1, 'en', '{de}', $2, 'm', '{}', 0)`, engineID, promptID); err != nil {
		t.Fatal(err)
	}
}

// A rebuild replaces the demo tenant. A visitor's token for the old one gets
// demo_reset, which the app answers by entering the new demo; leaving still
// works with it.
func TestIntegrationDemoOrgRebuildAnswersDemoReset(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)
	if err := f.q.DeleteTenant(context.Background(), f.demo.ID); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, "/api/projects", nil, token)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "demo_reset" {
		t.Errorf("a request with the old demo token = %d %s, want 403 demo_reset", rec.Code, rec.Body.String())
	}
	if rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/api/auth/demo/leave", nil, token); rec.Code != http.StatusOK {
		t.Errorf("leave with the old demo token = %d %s", rec.Code, rec.Body.String())
	}
}

// appConfigPlayground reads the playground flag the SPA gets from /api/config.
func appConfigPlayground(t *testing.T, router chi.Router) any {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("/api/config %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return body["playground"]
}

// With DEMO_ORG off the app offers no Playground, and entering is refused
// even while a demo organization from an earlier run is still in the database.
func TestIntegrationDemoOrgOff(t *testing.T) {
	f := newDemoFixture(t)
	if got := appConfigPlayground(t, f.router); got != true {
		t.Errorf("playground with DEMO_ORG on = %v, want true", got)
	}

	off := testutil.NewTestRouterWithoutDemoOrg(f.pool)
	if got := appConfigPlayground(t, off); got != false {
		t.Errorf("playground with DEMO_ORG off = %v, want false", got)
	}
	rec := testutil.DoRequest(t, off, http.MethodPost, "/api/auth/demo/enter", nil, f.owner.ID, f.customer.ID)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "demo_unavailable" {
		t.Errorf("enter with DEMO_ORG off = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(f.playbooksBody(t, f.router), `"demo-overview"`) {
		t.Error("saved tours missing with DEMO_ORG on")
	}
	if body := f.playbooksBody(t, off); strings.Contains(body, `"demo-overview"`) {
		t.Errorf("saved tours listed with DEMO_ORG off: %s", body)
	}
}

// playbooksBody is GET /api/chat/playbooks for the customer's owner.
func (f *demoFixture) playbooksBody(t *testing.T, router chi.Router) string {
	t.Helper()
	rec := testutil.DoRequest(t, router, http.MethodGet, "/api/chat/playbooks", nil, f.owner.ID, f.customer.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("playbooks: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
