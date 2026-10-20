package auth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// CreateOrgWithOwner is the first-run path of a self-hosted install
// (`server create-org`): no OAuth, no email, only password sign-in.

func countRows(t *testing.T, pool *pgxpool.Pool, table string, tenantID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE tenant_id = $1", tenantID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCreateOrgWithOwner_OwnerSignsInWithPassword(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant, user, err := auth.CreateOrgWithOwner(ctx, pool, "  Acme Translations ", "Owner@Acme.example.com", "first-run-pass")
	if err != nil {
		t.Fatalf("CreateOrgWithOwner: %v", err)
	}
	if tenant.Name != "Acme Translations" || tenant.Status != "approved" {
		t.Fatalf("expected an approved org named %q, got name=%q status=%q", "Acme Translations", tenant.Name, tenant.Status)
	}
	if user.Email != "owner@acme.example.com" {
		t.Fatalf("owner email must be normalized, got %q", user.Email)
	}
	m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil || m.Role != auth.RoleOwner {
		t.Fatalf("expected owner membership, got role=%q err=%v", m.Role, err)
	}

	// The password works through the real login route, and the session it
	// returns is accepted by an org-scoped route.
	router := newSignupRouter(pool, nil, nil)
	status, body := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "OWNER@acme.example.com", "password": "first-run-pass",
	})
	if status != http.StatusOK {
		t.Fatalf("login: expected 200, got %d (%v)", status, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("login returned no session token: %v", body)
	}
	if status, me := do(t, router, "GET", "/api/auth/me", token, nil); status != http.StatusOK {
		t.Fatalf("session from create-org owner rejected: %d (%v)", status, me)
	}

	if status, _ := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "owner@acme.example.com", "password": "wrong-password",
	}); status == http.StatusOK {
		t.Fatal("a wrong password must not sign in")
	}
}

// An org from `server create-org` starts with exactly what an org created in
// the operator console gets: both go through the same provisioning.
func TestCreateOrgWithOwner_ProvisionsLikePlatformCreate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	_, opToken := seedOperator(t, q)
	status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Console Org", "owner_email": "console-owner@example.com",
	})
	if status != http.StatusCreated {
		t.Fatalf("platform create: expected 201, got %d (%v)", status, body)
	}
	consoleID := int64(body["tenant"].(map[string]any)["id"].(float64))

	cli, _, err := auth.CreateOrgWithOwner(ctx, pool, "CLI Org", "cli-owner@example.com", "first-run-pass")
	if err != nil {
		t.Fatalf("CreateOrgWithOwner: %v", err)
	}

	for _, table := range []string{"mt_engines", "workflows", "dashboards"} {
		console, fromCLI := countRows(t, pool, table, consoleID), countRows(t, pool, table, cli.ID)
		if console == 0 {
			t.Fatalf("%s: the console org got none, so this comparison proves nothing", table)
		}
		if console != fromCLI {
			t.Errorf("%s: console org has %d, create-org org has %d", table, console, fromCLI)
		}
	}
	names := func(tenantID int64) []string {
		wfs, err := q.ListWorkflows(ctx, tenantID)
		if err != nil {
			t.Fatalf("list workflows: %v", err)
		}
		var out []string
		for _, w := range wfs {
			out = append(out, w.Name)
		}
		sort.Strings(out)
		return out
	}
	if a, b := names(consoleID), names(cli.ID); !slices.Equal(a, b) {
		t.Errorf("default workflows differ: console %v, create-org %v", a, b)
	}
}

// Bad input is refused before anything is written.
func TestCreateOrgWithOwner_RefusesBadInputAndCreatesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	existing := testutil.SeedTenant(t, q, "Existing Org")
	testutil.SeedUser(t, q, existing.ID, "taken@example.com", "some-password")

	tenantCount := func() int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM tenants").Scan(&n); err != nil {
			t.Fatalf("count tenants: %v", err)
		}
		return n
	}
	before := tenantCount()

	cases := []struct {
		name, org, email, password string
		wantExists                 bool
	}{
		{"empty name", "  ", "a@example.com", "long-enough-pw", false},
		{"bad email", "Org", "not-an-email", "long-enough-pw", false},
		{"short password", "Org", "b@example.com", "short", false},
		{"email already has an account", "Org", "Taken@example.com", "long-enough-pw", true},
	}
	for _, c := range cases {
		_, _, err := auth.CreateOrgWithOwner(ctx, pool, c.org, c.email, c.password)
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if got := errors.Is(err, auth.ErrAccountExists); got != c.wantExists {
			t.Errorf("%s: ErrAccountExists = %v, want %v (err: %v)", c.name, got, c.wantExists, err)
		}
	}
	if after := tenantCount(); after != before {
		t.Fatalf("refused calls must create no org: tenants %d -> %d", before, after)
	}
}

// A failure after the org is provisioned rolls everything back: no tenant, and
// none of the engine, dashboard or workflow rows provisioning wrote. A
// test-only trigger makes the owner INSERT fail, which runs after provisioning.
func TestCreateOrgWithOwner_FailureAfterProvisioningRollsBack(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`CREATE FUNCTION test_fail_owner_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test: owner insert refused'; END $$`)
	exec(`CREATE TRIGGER test_fail_owner_insert BEFORE INSERT ON users
		FOR EACH ROW WHEN (NEW.email = 'doomed-owner@example.com') EXECUTE FUNCTION test_fail_owner_insert()`)
	defer exec(`DROP TRIGGER test_fail_owner_insert ON users; DROP FUNCTION test_fail_owner_insert()`)

	tables := []string{"tenants", "mt_engines", "dashboards", "workflows"}
	countAll := func() map[string]int {
		out := map[string]int{}
		for _, table := range tables {
			var n int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			out[table] = n
		}
		return out
	}
	before := countAll()

	_, _, err := auth.CreateOrgWithOwner(ctx, pool, "Doomed Org", "doomed-owner@example.com", "first-run-pass")
	if err == nil || !strings.Contains(err.Error(), "create user") {
		t.Fatalf("expected the owner insert to fail after provisioning, got %v", err)
	}
	for table, n := range countAll() {
		if n != before[table] {
			t.Errorf("%s: %d rows before, %d after a failed create-org", table, before[table], n)
		}
	}
}
