package flowtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// vendorFixture is a client org with its owner, and a vendor org of that
// client with its admin. The router has issue reporting on, so every route
// a deployment can mount is walked.
type vendorFixture struct {
	router      chi.Router
	pool        *pgxpool.Pool
	q           db.Querier
	client      db.Tenant
	clientOwner db.User
	vendor      db.Tenant
	vendorAdmin db.User
}

func newVendorFixture(t *testing.T) *vendorFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router, _ := testutil.NewTestRouterWithIssues(pool, nil, []string{"operator@test.com"})
	f := &vendorFixture{router: router, pool: pool, q: q}
	f.client = testutil.SeedTenant(t, q, "Client Co")
	f.clientOwner = testutil.SeedUserWithRole(t, q, f.client.ID, "owner@client.example.com", "pass", "owner")
	f.vendor = testutil.SeedVendorTenant(t, q, f.client.ID, "Vendor Co")
	f.vendorAdmin = testutil.SeedUserWithRole(t, q, f.vendor.ID, "admin@vendor.example.com", "pass", "admin")
	return f
}

// asVendor sends a request with the vendor admin's session.
func (f *vendorFixture) asVendor(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, method, path, body, f.vendorAdmin.ID, f.vendor.ID, "admin")
}

// vendorAllowedRoutes is the vendor org's surface, written out route by
// route: every other org-scoped route must refuse a vendor org. The
// notification stream is mounted only with a broker, which the test router
// has not; the barrier's unit test covers it.
var vendorAllowedRoutes = map[string]bool{
	"POST /api/auth/dev-role":                              true,
	"GET /api/members":                                     true,
	"GET /api/members/page":                                true,
	"GET /api/members/facets":                              true,
	"GET /api/invites/accepted":                            true,
	"PUT /api/members/{userID}/role":                       true,
	"DELETE /api/members/{userID}":                         true,
	"POST /api/members/{userID}/enable":                    true,
	"GET /api/invites/":                                    true,
	"POST /api/invites/":                                   true,
	"DELETE /api/invites/{inviteID}":                       true,
	"POST /api/invites/{inviteID}/resend":                  true,
	"GET /api/users/{userID}/language-pairs":               true,
	"PUT /api/users/{userID}/language-pairs":               true,
	"GET /api/languages":                                   true,
	"GET /api/tasks":                                       true,
	"GET /api/tasks/steps":                                 true,
	"GET /api/projects/":                                   true,
	"GET /api/notifications/":                              true,
	"GET /api/notifications/count":                         true,
	"POST /api/notifications/read-all":                     true,
	"PATCH /api/notifications/{notificationID}/read":       true,
	"GET /api/notification-preferences":                    true,
	"PUT /api/notification-preferences":                    true,
	"GET /api/appearance":                                  true,
	"PUT /api/appearance":                                  true,
	"GET /api/docs/":                                       true,
	"GET /api/docs/*":                                      true,
	"GET /api/docs/assets/*":                               true,
	"GET /api/docs/search":                                 true,
	"GET /api/api-docs/":                                   true,
	"GET /api/api-docs/*":                                  true,
	"GET /api/api-docs/assets/*":                           true,
	"GET /api/api-docs/openapi.json":                       true,
	"GET /api/api-docs/search":                             true,
	"GET /api/issues":                                      true,
	"GET /api/issues/count":                                true,
	"POST /api/issues":                                     true,
	"GET /api/issues/{issueID}":                            true,
	"DELETE /api/issues/{issueID}":                         true,
	"POST /api/issues/{issueID}/comments":                  true,
	"POST /api/issues/{issueID}/confirm":                   true,
	"PATCH /api/issues/{issueID}/kind":                     true,
	"GET /api/issues/{issueID}/attachments/{attachmentID}": true,
}

// identityPrefixes hold the routes that need no org (the org picker, the
// profile, the platform operators' surface). They act on the user, not on an
// org, so the vendor barrier does not cover them; a new identity route
// elsewhere fails the walk until someone decides what a vendor org may do
// with it.
var identityPrefixes = []string{"/api/auth/", "/api/platform/", "/api/debug/"}

// Every org-scoped route either is on the vendor allow-list or answers 403
// vendor_org_restricted to a vendor org; a route added later is covered
// without touching this test.
func TestIntegrationVendorOrgRouteBarrier(t *testing.T) {
	f := newVendorFixture(t)
	identity, err := auth.GenerateIdentityToken(f.vendorAdmin.ID, testutil.TestJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	// A path id no row has, so the allowed routes touch nothing.
	const missingID = "999999999"
	checked, allowed := 0, 0
	seen := map[string]bool{}
	err = chi.Walk(f.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path := strings.TrimSuffix(pathParam.ReplaceAllString(route, missingID), "/*")
		if path == "" {
			path = "/"
		}
		// Public routes never see a tenant.
		anon := httptest.NewRecorder()
		f.router.ServeHTTP(anon, httptest.NewRequest(method, path, strings.NewReader("{}")))
		if anon.Code != http.StatusUnauthorized {
			if !vendorAllowedRoutes[method+" "+route] {
				return nil
			}
			// Docs answer anonymous callers too; with a session they pass
			// the barrier like every org-scoped route.
		} else {
			// An identity token reaches only the routes that need no org.
			rec := testutil.DoRequestWithRawToken(t, f.router, method, path, map[string]any{}, identity)
			if !strings.Contains(rec.Body.String(), "select an organization") {
				if !hasAnyPrefix(path, identityPrefixes) {
					t.Errorf("%s %s needs no org and is outside %v: decide what a vendor org may do with it", method, route, identityPrefixes)
				}
				return nil
			}
		}
		seen[method+" "+route] = true
		checked++
		rec := f.asVendor(t, method, path, map[string]any{})
		code := errorCode(t, rec)
		if vendorAllowedRoutes[method+" "+route] {
			allowed++
			if code == "vendor_org_restricted" || code == "client_inactive" {
				t.Errorf("%s %s is on the vendor allow-list but answers %d %s", method, route, rec.Code, rec.Body.String())
			}
			return nil
		}
		if rec.Code != http.StatusForbidden || code != "vendor_org_restricted" {
			t.Errorf("%s %s for a vendor org = %d %s, want 403 vendor_org_restricted", method, route, rec.Code, rec.Body.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 300 {
		t.Fatalf("only %d org-scoped routes checked; the walk did not see the router", checked)
	}
	for route := range vendorAllowedRoutes {
		if !seen[route] && !strings.HasPrefix(route, "POST /api/auth/") {
			t.Errorf("allow-listed %s was not walked as an org-scoped route: the list is stale", route)
		}
	}
	if allowed == 0 {
		t.Fatal("no allow-listed route was walked")
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// The MCP server, the assistant and /api/v1 are closed to a vendor org on
// every credential path: session, access key and OAuth token.
func TestIntegrationVendorOrgClosedSurfaces(t *testing.T) {
	f := newVendorFixture(t)
	mcpBody := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/mcp"},
		{http.MethodPost, "/api/chat/"},
		{http.MethodGet, "/api/chat/conversations"},
		{http.MethodGet, "/api/v1/projects/"},
		{http.MethodGet, "/api/v1/members/"},
		{http.MethodGet, "/api/v1/openapi.json"},
	} {
		rec := f.asVendor(t, c.method, c.path, mcpBody)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "vendor_org_restricted" {
			t.Errorf("session %s %s = %d %s, want 403 vendor_org_restricted", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	key := testutil.SeedAPIToken(t, f.q, f.vendor.ID, f.vendorAdmin.ID, "write", time.Hour)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/api/v1/projects/"},
		{http.MethodGet, "/api/v1/invites/"},
	} {
		rec := testutil.DoRequestWithRawToken(t, f.router, c.method, c.path, mcpBody, key)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "vendor_org_restricted" {
			t.Errorf("access key %s %s = %d %s, want 403 vendor_org_restricted", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	oauth := testutil.SeedOAuthToken(t, f.q, f.vendor.ID, f.vendorAdmin.ID, "write")
	rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodPost, "/mcp", mcpBody, oauth)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "vendor_org_restricted" {
		t.Errorf("OAuth /mcp = %d %s, want 403 vendor_org_restricted", rec.Code, rec.Body.String())
	}
}

// A vendor org works only while its client org is approved: a suspended,
// pending or closing client shuts it out, allowed routes included.
func TestIntegrationVendorOrgNeedsActiveClient(t *testing.T) {
	f := newVendorFixture(t)
	ctx := context.Background()
	if rec := f.asVendor(t, http.MethodGet, "/api/tasks", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tasks with an active client = %d %s", rec.Code, rec.Body.String())
	}
	setStatus := func(status string) {
		t.Helper()
		if _, err := f.q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: f.client.ID, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"disabled", "pending", "rejected"} {
		setStatus(status)
		for _, path := range []string{"/api/tasks", "/api/members", "/api/tms"} {
			rec := f.asVendor(t, http.MethodGet, path, nil)
			if rec.Code != http.StatusForbidden || errorCode(t, rec) != "client_inactive" {
				t.Errorf("client %s: GET %s = %d %s, want 403 client_inactive", status, path, rec.Code, rec.Body.String())
			}
		}
	}
	setStatus("approved")
	if rec := f.asVendor(t, http.MethodGet, "/api/tasks", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /api/tasks after the client is approved again = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.q.MarkTenantDeleting(ctx, f.client.ID); err != nil {
		t.Fatal(err)
	}
	if rec := f.asVendor(t, http.MethodGet, "/api/tasks", nil); rec.Code != http.StatusForbidden || errorCode(t, rec) != "client_inactive" {
		t.Errorf("client deleting: GET /api/tasks = %d %s, want 403 client_inactive", rec.Code, rec.Body.String())
	}
	// The vendor org's own status still comes first.
	if _, err := f.q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: f.vendor.ID, Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if rec := f.asVendor(t, http.MethodGet, "/api/tasks", nil); rec.Code != http.StatusForbidden || errorCode(t, rec) != "org_pending" {
		t.Errorf("vendor disabled: GET /api/tasks = %d %s, want 403 org_pending", rec.Code, rec.Body.String())
	}
}

// The barrier leaves the client org alone: its owner keeps the whole app,
// the MCP server included.
func TestIntegrationVendorOrgClientUnaffected(t *testing.T) {
	f := newVendorFixture(t)
	rec := testutil.DoRequest(t, f.router, http.MethodPost, "/api/tms",
		map[string]any{"name": "Client TM", "source_language": "en", "target_languages": []string{"de"}}, f.clientOwner.ID, f.client.ID)
	if rec.Code >= 400 {
		t.Errorf("client POST /api/tms = %d %s", rec.Code, rec.Body.String())
	}
	rec = testutil.DoRequest(t, f.router, http.MethodPost, "/mcp",
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, f.clientOwner.ID, f.client.ID)
	if rec.Code == http.StatusForbidden {
		t.Errorf("client POST /mcp = %d %s", rec.Code, rec.Body.String())
	}
}

// A vendor admin reaches none of the client's data: not its projects, jobs,
// files or members, and not through the routes the vendor org may use.
func TestIntegrationVendorOrgCannotReachClientData(t *testing.T) {
	f := newVendorFixture(t)
	ctx := context.Background()
	project := testutil.SeedProject(t, f.q, f.client.ID, "Client launch", "en", []string{"de"})
	file := testutil.SeedFile(t, f.q, project.ID, "client.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, f.q, file.ID, "de")
	linguist := testutil.SeedUserWithRole(t, f.q, f.client.ID, "linguist@client.example.com", "pass", "linguist")
	testutil.SeedJobStep(t, f.q, project.SettingsProfileID, job.ID, &linguist.ID)
	invite, err := f.q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: f.client.ID, Email: "new@client.example.com", Role: "linguist", InvitedBy: f.clientOwner.ID,
		Token: "client-invite-token", Kind: auth.InviteKindMember,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Client routes by id: refused or not found, never the data.
	for _, path := range []string{
		fmt.Sprintf("/api/projects/%d", project.ID),
		fmt.Sprintf("/api/projects/%d/files", project.ID),
		fmt.Sprintf("/api/files/%d", file.ID),
		fmt.Sprintf("/api/jobs/%d", job.ID),
		fmt.Sprintf("/api/jobs/%d/segments", job.ID),
	} {
		rec := f.asVendor(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %s, want 403 or 404", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "Client launch") || strings.Contains(rec.Body.String(), "client.txt") {
			t.Errorf("GET %s leaks client data: %s", path, rec.Body.String())
		}
	}

	// The allowed lists show the vendor org's own rows only.
	for _, path := range []string{"/api/projects", "/api/members", "/api/tasks", "/api/invites/"} {
		rec := f.asVendor(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d %s", path, rec.Code, rec.Body.String())
			continue
		}
		for _, leak := range []string{"Client launch", "client.txt", "client.example.com"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("GET %s shows the client's %q: %s", path, leak, rec.Body.String())
			}
		}
	}

	// The allowed writes cannot reach a client member or invite.
	rec := f.asVendor(t, http.MethodPut, fmt.Sprintf("/api/members/%d/role", linguist.ID), map[string]any{"role": "admin"})
	if rec.Code < 400 {
		t.Errorf("vendor changed a client member's role: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.asVendor(t, http.MethodDelete, fmt.Sprintf("/api/members/%d", linguist.ID), nil)
	if rec.Code < 400 {
		t.Errorf("vendor removed a client member: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.asVendor(t, http.MethodPut, fmt.Sprintf("/api/users/%d/language-pairs", linguist.ID),
		map[string]any{"pairs": []map[string]string{{"source_lang": "en", "target_lang": "fr"}}})
	if rec.Code < 400 {
		t.Errorf("vendor replaced a client member's language pairs: %d %s", rec.Code, rec.Body.String())
	}
	m, err := f.q.GetMembership(ctx, db.GetMembershipParams{UserID: linguist.ID, TenantID: f.client.ID})
	if err != nil || m.Role != "linguist" {
		t.Errorf("client linguist's membership after the vendor's writes = %+v (%v)", m, err)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, fmt.Sprintf("/api/invites/%d", invite.ID)},
		{http.MethodPost, fmt.Sprintf("/api/invites/%d/resend", invite.ID)},
	} {
		if rec := f.asVendor(t, c.method, c.path, nil); rec.Code < 400 {
			t.Errorf("vendor %s %s on a client invite = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if n := testutil.CountRows(t, f.pool, `SELECT count(*) FROM invites WHERE id = $1`, invite.ID); n != 1 {
		t.Error("the vendor revoked the client's invite")
	}
}
