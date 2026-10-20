package template_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type templateMeta struct {
	Data struct {
		ID              int64    `json:"id"`
		Name            string   `json:"name"`
		ProjectName     *string  `json:"project_name"`
		SourceLanguage  *string  `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
		CustomerID      *int64   `json:"customer_id"`
		Domain          *string  `json:"domain"`
	} `json:"data"`
}

func seedCustomer(t *testing.T, q db.Querier, tenantID int64, name string) db.Customer {
	t.Helper()
	c, err := q.CreateCustomer(context.Background(), db.CreateCustomerParams{TenantID: tenantID, Name: name})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	return c
}

// A template carries a customer and a domain: set on create, changed with the
// same tri-state PATCH semantics as the other scalars, copied by duplicate, and
// never a customer of another tenant.
func TestIntegrationTemplateCustomerAndDomain(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MetaCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "meta@test.com", "pass")
	customer := seedCustomer(t, q, tenant.ID, "Nordwind")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	foreign := seedCustomer(t, q, other.ID, "Foreign")

	// Create with all three; free text is stored trimmed.
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Travel", "project_name": " Summer campaign ", "customer_id": customer.ID, "domain": "  travel  ",
		"source_language": "EN", "target_languages": []string{"de", "FR", "de"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created templateMeta
	testutil.DecodeJSON(t, rec, &created)
	if created.Data.ProjectName == nil || *created.Data.ProjectName != "Summer campaign" {
		t.Fatalf("project_name: got %v, want %q", created.Data.ProjectName, "Summer campaign")
	}
	// Languages follow the project rules: normalized and deduplicated.
	if created.Data.SourceLanguage == nil || *created.Data.SourceLanguage != "en" || fmt.Sprint(created.Data.TargetLanguages) != "[de fr]" {
		t.Fatalf("languages: got %v %v, want en [de fr]", created.Data.SourceLanguage, created.Data.TargetLanguages)
	}
	// A target equal to the source is refused.
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Bad", "source_language": "en", "target_languages": []string{"en"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if created.Data.CustomerID == nil || *created.Data.CustomerID != customer.ID {
		t.Fatalf("customer_id: got %v, want %d", created.Data.CustomerID, customer.ID)
	}
	if created.Data.Domain == nil || *created.Data.Domain != "travel" {
		t.Fatalf("domain: got %v, want %q", created.Data.Domain, "travel")
	}
	id := created.Data.ID

	// GET returns them too (the response is built from the template row).
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", id), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	// The detail endpoint nests the template row under "template".
	var detail struct {
		Data struct {
			Template struct {
				CustomerID *int64  `json:"customer_id"`
				Domain     *string `json:"domain"`
			} `json:"template"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &detail)
	if detail.Data.Template.CustomerID == nil || *detail.Data.Template.CustomerID != customer.ID || detail.Data.Template.Domain == nil || *detail.Data.Template.Domain != "travel" {
		t.Fatalf("detail lost metadata: %+v", detail.Data.Template)
	}

	// A PATCH that says nothing about them keeps both.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), map[string]any{
		"mt_on_upload": false,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var patched templateMeta
	testutil.DecodeJSON(t, rec, &patched)
	if patched.Data.CustomerID == nil || patched.Data.Domain == nil || patched.Data.ProjectName == nil || patched.Data.SourceLanguage == nil || len(patched.Data.TargetLanguages) != 2 {
		t.Fatalf("partial PATCH cleared metadata: %+v", patched.Data)
	}
	// Changing only the source is checked against the stored targets.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), map[string]any{
		"source_language": "de",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// A customer of another tenant is refused, and nothing changes.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), map[string]any{
		"customer_id": foreign.ID,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	row, err := q.GetTemplate(context.Background(), db.GetTemplateParams{ID: id, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if row.CustomerID == nil || *row.CustomerID != customer.ID {
		t.Fatalf("cross-tenant customer leaked onto the template: %v", row.CustomerID)
	}
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Leak", "customer_id": foreign.ID,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Duplicate carries both.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/templates/%d/duplicate", id), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var dup templateMeta
	testutil.DecodeJSON(t, rec, &dup)
	if dup.Data.CustomerID == nil || *dup.Data.CustomerID != customer.ID || dup.Data.Domain == nil || *dup.Data.Domain != "travel" || dup.Data.ProjectName == nil || *dup.Data.ProjectName != "Summer campaign" || dup.Data.SourceLanguage == nil || len(dup.Data.TargetLanguages) != 2 {
		t.Fatalf("duplicate dropped metadata: %+v", dup.Data)
	}

	// null clears the customer; empty text clears the domain, project name and
	// source; an empty list clears the targets.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/templates/%d", id), map[string]any{
		"customer_id": nil, "domain": "   ", "project_name": "", "source_language": "", "target_languages": []string{},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &patched)
	if patched.Data.CustomerID != nil || patched.Data.Domain != nil || patched.Data.ProjectName != nil || patched.Data.SourceLanguage != nil || len(patched.Data.TargetLanguages) != 0 {
		t.Fatalf("clear did not clear: %+v", patched.Data)
	}

	// A template made without a project name starts with its own name as the
	// project name.
	rec = testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{"name": "Bare"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var bare templateMeta
	testutil.DecodeJSON(t, rec, &bare)
	if bare.Data.ProjectName == nil || *bare.Data.ProjectName != "Bare" {
		t.Fatalf("project_name should default to the template name, got %v", bare.Data.ProjectName)
	}
}
