// Creator attribution for term bases: the create handler records who made the
// base, and the list endpoint resolves that id to a display name so the
// terminology list can show ownership without a second round trip.
package terminology_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// termBaseListRow is the slice of the list payload this test cares about.
type termBaseListRow struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	CreatedBy     *int64 `json:"created_by"`
	CreatedByName string `json:"created_by_name"`
}

func listTermBases(t *testing.T, router http.Handler, userID, tenantID int64) []termBaseListRow {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", "/api/terminology", nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []termBaseListRow `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}

func TestIntegrationTermBaseCreatorAttribution(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AttribCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "creator@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Owned Base",
		"source_language": "en",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rows := listTermBases(t, router, user.ID, tenant.ID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 base, got %d", len(rows))
	}
	if rows[0].CreatedBy == nil || *rows[0].CreatedBy != user.ID {
		t.Errorf("created_by = %v, want %d", rows[0].CreatedBy, user.ID)
	}
	if rows[0].CreatedByName != user.Name {
		t.Errorf("created_by_name = %q, want %q", rows[0].CreatedByName, user.Name)
	}
}

// A base created before attribution existed (created_by NULL) must list with an
// empty name rather than erroring or inventing an owner — the FE renders "—".
func TestIntegrationTermBaseCreatorUnattributed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LegacyCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "viewer@test.com", "pass")

	if _, err := q.CreateTermBase(context.Background(), db.CreateTermBaseParams{
		TenantID:       tenant.ID,
		Name:           "Legacy Base",
		SourceLanguage: "en",
		CreatedBy:      nil,
	}); err != nil {
		t.Fatalf("create legacy base: %v", err)
	}

	rows := listTermBases(t, router, user.ID, tenant.ID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 base, got %d", len(rows))
	}
	if rows[0].CreatedBy != nil {
		t.Errorf("created_by = %v, want nil", rows[0].CreatedBy)
	}
	if rows[0].CreatedByName != "" {
		t.Errorf("created_by_name = %q, want empty", rows[0].CreatedByName)
	}
}

// users.name defaults to the empty string (nothing forces a display name at
// signup), so the list must fall back to the email instead of rendering a
// blank creator.
func TestIntegrationTermBaseCreatorNameFallsBackToEmail(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NamelessCorp")

	// SeedUser sets name = email, so build the nameless identity directly.
	nameless, err := q.CreateUser(ctx, db.CreateUserParams{Email: "nameless@test.com", Name: ""})
	if err != nil {
		t.Fatalf("create nameless user: %v", err)
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{
		UserID: nameless.ID, TenantID: tenant.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Nameless Base",
		"source_language": "en",
	}, nameless.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rows := listTermBases(t, router, nameless.ID, tenant.ID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 base, got %d", len(rows))
	}
	if rows[0].CreatedByName != "nameless@test.com" {
		t.Errorf("created_by_name = %q, want the email fallback", rows[0].CreatedByName)
	}
}
