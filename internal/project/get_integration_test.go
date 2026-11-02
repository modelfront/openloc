package project_test

// GET /api/projects/{id} is what the project page reads: the same row as the
// project list (creator name included), for one project of the caller's org.
//
// Skipped with -short.

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

func TestIntegrationProjectGetReturnsTheListRow(t *testing.T) {
	f, cleanup := newDraftFixture(t, "ProjectGet")
	defer cleanup()

	rec := testutil.DoRequestWithRole(t, f.router, "POST", "/api/projects", map[string]any{
		"name": "Get me", "source_language": "en", "target_languages": []string{"fr"},
		"workflow_id": testutil.LibraryWorkflow(t, f.q, f.tenant.ID),
	}, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created projectResp
	testutil.DecodeJSON(t, rec, &created)
	id := created.Data.ID

	customer := testutil.SeedProjectSystemLabel(t, f.q, f.tenant.ID, id, "customer", "Acme Travel")

	rec = testutil.DoRequestWithRole(t, f.router, "GET", "/api/projects/"+itoa(id), nil, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got struct {
		Data struct {
			ID              int64  `json:"id"`
			Name            string `json:"name"`
			CreatedByName   string `json:"created_by_name"`
			CustomerName    string `json:"customer_name"`
			CustomerValueID *int64 `json:"customer_value_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.ID != id || got.Data.Name != "Get me" {
		t.Fatalf("got project %d %q, want %d %q", got.Data.ID, got.Data.Name, id, "Get me")
	}
	if got.Data.CreatedByName != f.user.Email {
		t.Errorf("created_by_name = %q, want the creator %q", got.Data.CreatedByName, f.user.Email)
	}
	if got.Data.CustomerName != "Acme Travel" || got.Data.CustomerValueID == nil || *got.Data.CustomerValueID != customer.ID {
		t.Errorf("customer = %q / %v, want %q / %d", got.Data.CustomerName, got.Data.CustomerValueID, "Acme Travel", customer.ID)
	}

	other := testutil.SeedTenant(t, f.q, "ProjectGetOther")
	stranger := testutil.SeedUserWithRole(t, f.q, other.ID, "pm@projectgetother.test", "pass", "project_manager")
	rec = testutil.DoRequestWithRole(t, f.router, "GET", "/api/projects/"+itoa(id), nil, stranger.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// A deleted project is gone from the page as from the list.
	if _, err := f.pool.Exec(context.Background(), "UPDATE projects SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoRequestWithRole(t, f.router, "GET", "/api/projects/"+itoa(id), nil, f.user.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}
