// Creator attribution for TMs: the create handler records who made the
// collection, and the list endpoint resolves that id to a display name so the
// TM list can show ownership without a second round trip.
package translationmemory_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type tmListRow struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	CreatedBy     *int64 `json:"created_by"`
	CreatedByName string `json:"created_by_name"`
}

func listTMs(t *testing.T, router http.Handler, userID, tenantID int64) []tmListRow {
	t.Helper()
	rec := testutil.DoRequest(t, router, "GET", "/api/tms", nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []tmListRow `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}

func TestIntegrationTMCreatorAttribution(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AttribCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tm-creator@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{
		"name":             "Owned TM",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rows := listTMs(t, router, user.ID, tenant.ID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 TM, got %d", len(rows))
	}
	if rows[0].CreatedBy == nil || *rows[0].CreatedBy != user.ID {
		t.Errorf("created_by = %v, want %d", rows[0].CreatedBy, user.ID)
	}
	if rows[0].CreatedByName != user.Name {
		t.Errorf("created_by_name = %q, want %q", rows[0].CreatedByName, user.Name)
	}
}

// A TM created before attribution existed (created_by NULL) must list with an
// empty name rather than erroring or inventing an owner — the FE renders "—".
func TestIntegrationTMCreatorUnattributed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LegacyCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tm-viewer@test.com", "pass")

	src := "en"
	if _, err := q.CreateTMCollection(context.Background(), db.CreateTMCollectionParams{
		Name:            "Legacy TM",
		TenantID:        tenant.ID,
		SourceLanguage:  &src,
		TargetLanguages: []string{"es"},
		CreatedBy:       nil,
	}); err != nil {
		t.Fatalf("create legacy TM: %v", err)
	}

	rows := listTMs(t, router, user.ID, tenant.ID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 TM, got %d", len(rows))
	}
	if rows[0].CreatedBy != nil {
		t.Errorf("created_by = %v, want nil", rows[0].CreatedBy)
	}
	if rows[0].CreatedByName != "" {
		t.Errorf("created_by_name = %q, want empty", rows[0].CreatedByName)
	}
}
