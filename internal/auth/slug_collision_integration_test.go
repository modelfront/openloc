package auth_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

// Every org-creation path runs its tenant INSERT inside a transaction. A second
// org with the same name must get a suffixed slug, not fail.

func slugsNamed(t *testing.T, pool *pgxpool.Pool, name string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "SELECT slug FROM tenants WHERE name = $1 ORDER BY id", name)
	if err != nil {
		t.Fatalf("list slugs: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan slug: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list slugs: %v", err)
	}
	return out
}

func TestSameOrgNameGetsSuffixedSlug_CreateOrgWithOwner(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	if _, _, err := auth.CreateOrgWithOwner(ctx, pool, "Twin Org", "first@example.com", "first-run-pass"); err != nil {
		t.Fatalf("first org: %v", err)
	}
	if _, _, err := auth.CreateOrgWithOwner(ctx, pool, "Twin Org", "second@example.com", "first-run-pass"); err != nil {
		t.Fatalf("second org with the same name: %v", err)
	}
	if got, want := slugsNamed(t, pool, "Twin Org"), []string{"twin-org", "twin-org-2"}; !slices.Equal(got, want) {
		t.Fatalf("slugs = %v, want %v", got, want)
	}
}

func TestSameOrgNameGetsSuffixedSlug_PlatformCreate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	_, opToken := seedOperator(t, q)

	for i, owner := range []string{"first@example.com", "second@example.com"} {
		status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
			"name": "Twin Org", "owner_email": owner,
		})
		if status != http.StatusCreated {
			t.Fatalf("org %d: expected 201, got %d (%v)", i+1, status, body)
		}
	}
	if got, want := slugsNamed(t, pool, "Twin Org"), []string{"twin-org", "twin-org-2"}; !slices.Equal(got, want) {
		t.Fatalf("slugs = %v, want %v", got, want)
	}
}

func TestSameOrgNameGetsSuffixedSlug_SelfServeSignup(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"ops@example.com"}, &fakeSender{})

	for i, email := range []string{"owner@twin-one.com", "owner@twin-two.com"} {
		user := testutil.SeedIdentity(t, q, email)
		status, body := do(t, router, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), map[string]any{"name": "Twin Org"})
		if status != http.StatusCreated {
			t.Fatalf("signup %d: expected 201, got %d (%v)", i+1, status, body)
		}
	}
	if got, want := slugsNamed(t, pool, "Twin Org"), []string{"twin-org", "twin-org-2"}; !slices.Equal(got, want) {
		t.Fatalf("slugs = %v, want %v", got, want)
	}
}
