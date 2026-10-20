// Project metadata integration tests:
//   - HandleCreate: customer_id + status + auto-populated created_by
//   - HandleUpdateMetadata: tri-state PATCH (name, description, customer_id, status)
//   - HandleList: filters + cursor pagination
//
// Tenant isolation, FK validation, enum validation, and BACKWARDS COMPAT for
// the FE renameProject helper (which sends only `{name}` to the same PATCH
// route) are all covered.
package project_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"openloc/internal/testutil"
)

type listResp struct {
	Data []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		CustomerID *int64 `json:"customer_id"`
		CreatedBy  *int64 `json:"created_by"`
		// Resolved creator display name (LEFT JOIN users in
		// ListProjectsFiltered) — what the project list renders.
		CreatedByName string `json:"created_by_name"`
	} `json:"data"`
	Cursor  int64 `json:"cursor"`
	HasMore bool  `json:"has_more"`
}

type projResp struct {
	Data struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		CustomerID *int64  `json:"customer_id"`
		CreatedBy  *int64  `json:"created_by"`
		Deadline   *string `json:"deadline"` // RFC 3339 string or null
	} `json:"data"`
}

func createProject(t *testing.T, router any, body map[string]any, userID, tenantID int64) (int64, string) {
	t.Helper()
	rec := testutil.DoRequest(t, router.(http.Handler), "POST", "/api/projects", body, userID, tenantID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var p projResp
	testutil.DecodeJSON(t, rec, &p)
	return p.Data.ID, p.Data.Status
}

func TestIntegrationProjectCreateWithCustomerAndStatus(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// Seed a customer via the API (proves the create flow)
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Acme Corp",
	}, user.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var custResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &custResp)
	customerID := custResp.Data.ID

	// Create a project linked to the customer + draft status
	pid, status := createProject(t, router, map[string]any{
		"name":             "Acme Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
		"customer_id":      customerID,
		"status":           "draft",
	}, user.ID, tenant.ID)
	if status != "draft" {
		t.Errorf("expected status=draft, got %q", status)
	}

	// Verify created_by + customer_id set
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d", pid), nil, user.ID, tenant.ID)
	var got projResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.CustomerID == nil || *got.Data.CustomerID != customerID {
		t.Errorf("customer_id: got %v want %d", got.Data.CustomerID, customerID)
	}
	if got.Data.CreatedBy == nil || *got.Data.CreatedBy != user.ID {
		t.Errorf("created_by: got %v want %d", got.Data.CreatedBy, user.ID)
	}

	// Default status when omitted = in_progress (DB default)
	_, defaultStatus := createProject(t, router, map[string]any{
		"name":             "Plain Project",
		"source_language":  "en",
		"target_languages": []string{"de"},
	}, user.ID, tenant.ID)
	if defaultStatus != "in_progress" {
		t.Errorf("default status: got %q want in_progress", defaultStatus)
	}
}

func TestIntegrationProjectCreateValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// Invalid status enum → 400
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
		"status": "foo_bar",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Non-existent customer_id → 400 (NOT 500)
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "P2", "source_language": "en", "target_languages": []string{"fr"},
		"customer_id": 999999,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// customer_id from another tenant → 400 (cross-tenant attack returns the
	// same 400 as "not found" — no information leak)
	otherTenant := testutil.SeedTenant(t, q, "OtherCorp")
	otherOwner := testutil.SeedUserWithRole(t, q, otherTenant.ID, "other@test.com", "pass", "owner")
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{
		"name": "Other's Customer",
	}, otherOwner.ID, otherTenant.ID, "owner")
	var other struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &other)

	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "Cross Tenant", "source_language": "en", "target_languages": []string{"fr"},
		"customer_id": other.Data.ID, // exists, but in OtherCorp
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationProjectUpdateMetadata(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// Setup: customer + project linked to it
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/customers", map[string]string{"name": "Acme"}, user.ID, tenant.ID, "owner")
	var c struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &c)

	pid, _ := createProject(t, router, map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
		"customer_id": c.Data.ID,
	}, user.ID, tenant.ID)

	// PATCH name only — backwards-compat with FE renameProject helper
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"name": "Renamed"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got projResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Name != "Renamed" {
		t.Errorf("rename: got %q", got.Data.Name)
	}
	// customer_id should be UNCHANGED (absent from body = keep)
	if got.Data.CustomerID == nil || *got.Data.CustomerID != c.Data.ID {
		t.Errorf("rename should not clear customer_id, got %v", got.Data.CustomerID)
	}

	// PATCH status only
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"status": "completed"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Status != "completed" {
		t.Errorf("status: got %q", got.Data.Status)
	}

	// PATCH customer_id = null clears
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"customer_id": nil}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.CustomerID != nil {
		t.Errorf("clear customer_id: got %v want nil", got.Data.CustomerID)
	}

	// Invalid status → 400 (project unchanged)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"status": "totally_made_up"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Empty name → 400
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"name": "  "}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// PATCH customer_id = 0 also clears (legacy compat with qa_profile_id pattern)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"customer_id": c.Data.ID}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"customer_id": 0}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.CustomerID != nil {
		t.Errorf("customer_id=0 should clear, got %v", got.Data.CustomerID)
	}

	// PATCH customer_id = -1 → 400
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"customer_id": -1}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// PATCH description: empty string is VALID (unlike name) — clears the description
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"description": ""}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	// Verify via GET (PATCH response only includes the row, but we want to read after)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d", pid), nil, user.ID, tenant.ID)
	var detail struct {
		Data struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &detail)
	if detail.Data.Description != "" {
		t.Errorf("empty description should set empty, got %q", detail.Data.Description)
	}

	// All-fields-at-once: tests the COALESCE chain doesn't drop any field
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{
			"name":        "All Fields",
			"description": "all updated together",
			"status":      "on_hold",
			"customer_id": c.Data.ID,
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Name != "All Fields" || got.Data.Status != "on_hold" || got.Data.CustomerID == nil || *got.Data.CustomerID != c.Data.ID {
		t.Errorf("all-fields PATCH dropped a field: name=%q status=%q customer_id=%v", got.Data.Name, got.Data.Status, got.Data.CustomerID)
	}

	// --- deadline tri-state (slice 4b) ---
	// Same shape as customer_id: absent → keep, null → clear, ISO 8601 → set.
	deadlineISO := "2026-12-31T17:00:00Z"
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": deadlineISO}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline == nil {
		t.Fatalf("set deadline: got nil, want non-nil")
	}
	// pgtype.Timestamptz.MarshalJSON uses RFC3339Nano which preserves the
	// instant; we just check it round-trips to a parseable RFC 3339.
	if _, err := time.Parse(time.RFC3339Nano, *got.Data.Deadline); err != nil {
		t.Errorf("set deadline: response %q not RFC 3339: %v", *got.Data.Deadline, err)
	}

	// Absent deadline in PATCH → keep current
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"name": "name only"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline == nil {
		t.Errorf("absent deadline should keep current, got nil")
	}

	// Explicit null → clear
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": nil}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline != nil {
		t.Errorf("clear deadline (null): got %v, want nil", *got.Data.Deadline)
	}

	// Empty string → clear (mirrors null per handler comment)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": deadlineISO}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"deadline": ""}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline != nil {
		t.Errorf("clear deadline (empty string): got %v, want nil", *got.Data.Deadline)
	}

	// Garbage string → 400
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"deadline": "next Tuesday"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Non-string non-null (e.g. number) → 400
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": 42}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// Pin the backend invariant explicitly: PATCHing one project's status must
// not change any other project's status on the list view: PATCH /projects/A {status: 'in_progress'} must
// leave projects B and C UNCHANGED. If this test passes, the bug must be
// in the FE rendering (cache lag / shared-state in render), not the API.
//
// Anti-tautology RED proof: drop the `WHERE id = $1` clause from
// UpdateProjectMetadata's SQL. Every project under the tenant would
// inherit the new status, and the B/C assertions below would fail with
// "expected completed, got in_progress".
func TestIntegrationProjectUpdateMetadata_DoesNotAffectOtherProjects(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_ = q

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InScopeCo")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@scope.test", "pass", "owner")

	// Three projects, all starting at status='completed' (a state that's
	// distinct from the status we PATCH to, so any cross-project leak shows).
	pidA, _ := createProject(t, router, map[string]any{
		"name": "A", "source_language": "en", "target_languages": []string{"fr"},
		"status": "completed",
	}, user.ID, tenant.ID)
	pidB, _ := createProject(t, router, map[string]any{
		"name": "B", "source_language": "en", "target_languages": []string{"de"},
		"status": "completed",
	}, user.ID, tenant.ID)
	pidC, _ := createProject(t, router, map[string]any{
		"name": "C", "source_language": "en", "target_languages": []string{"es"},
		"status": "completed",
	}, user.ID, tenant.ID)

	// PATCH only project A to in_progress.
	rec := testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pidA),
		map[string]string{"status": "in_progress"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Read each project back and assert the scoped change.
	check := func(pid int64, want string) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d", pid), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var got projResp
		testutil.DecodeJSON(t, rec, &got)
		if got.Data.Status != want {
			t.Errorf("project %d status = %q, want %q (cross-project leak?)", pid, got.Data.Status, want)
		}
	}
	check(pidA, "in_progress")
	check(pidB, "completed")
	check(pidC, "completed")

	// Also verify via the LIST endpoint (the FE's actual data source).
	rec = testutil.DoRequest(t, router, "GET", "/api/projects", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list listResp
	testutil.DecodeJSON(t, rec, &list)
	statuses := map[int64]string{}
	for _, p := range list.Data {
		statuses[p.ID] = p.Status
	}
	if statuses[pidA] != "in_progress" {
		t.Errorf("list: A = %q, want in_progress", statuses[pidA])
	}
	if statuses[pidB] != "completed" {
		t.Errorf("list: B = %q, want completed", statuses[pidB])
	}
	if statuses[pidC] != "completed" {
		t.Errorf("list: C = %q, want completed", statuses[pidC])
	}
}

// Pins that the server accepts the deadline format it emits: a read-then-write
// round-trip with fractional seconds succeeds. Catches a switch to a stricter
// custom layout.
func TestIntegrationProjectDeadlineRoundTripWithFractionalSeconds(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	pid, _ := createProject(t, router, map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
	}, user.ID, tenant.ID)

	// Send a deadline WITH fractional seconds — what pgtype.Timestamptz emits
	// when the underlying value has microsecond precision (and what any DB
	// row inserted via raw SQL would carry).
	rec := testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": "2026-12-31T17:00:00.123456Z"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got projResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline == nil {
		t.Fatalf("set with fractional seconds: deadline = nil, want non-nil")
	}

	// Echo back the server's representation to prove the round-trip works
	// even when the stored value carries fractional seconds.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"deadline": *got.Data.Deadline}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// Pins that a deadline can be set at create time: whoever can create a
// project can set its initial deadline.
func TestIntegrationProjectCreateWithDeadline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// With deadline
	deadlineISO := "2026-09-15T17:00:00+02:00"
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "WithDeadline",
		"source_language":  "en",
		"target_languages": []string{"fr"},
		"deadline":         deadlineISO,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var got projResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline == nil {
		t.Fatalf("create with deadline: response Deadline = nil, want non-nil")
	}
	parsed, err := time.Parse(time.RFC3339Nano, *got.Data.Deadline)
	if err != nil {
		t.Fatalf("create with deadline: response %q not parseable: %v", *got.Data.Deadline, err)
	}
	want, _ := time.Parse(time.RFC3339, deadlineISO)
	if !parsed.Equal(want) {
		t.Errorf("create with deadline: stored %v, want %v (same instant)", parsed, want)
	}

	// Without deadline → null
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "NoDeadline",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Deadline != nil {
		t.Errorf("create without deadline: got %v, want nil", *got.Data.Deadline)
	}

	// Garbage deadline → 400
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "BadDeadline",
		"source_language":  "en",
		"target_languages": []string{"fr"},
		"deadline":         "yesterday",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationProjectFiltersAndPagination(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	// A customer is a value of the built-in Customer label,
	// and the list filters on that value.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/labels", nil, user.ID, tenant.ID, "owner")
	var labels struct {
		Data []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			System bool   `json:"system"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &labels)
	var customerLabelID int64
	for _, l := range labels.Data {
		if l.System && l.Name == "Customer" {
			customerLabelID = l.ID
		}
	}
	if customerLabelID == 0 {
		t.Fatal("tenant should be born with a Customer label")
	}
	rec = testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/labels/%d/values", customerLabelID),
		map[string]any{"value": "Acme"}, user.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var c struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &c)

	acme := func(projectID int64) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "PUT",
			fmt.Sprintf("/api/projects/%d/labels", projectID),
			map[string]any{"labels": []map[string]int64{
				{"label_definition_id": customerLabelID, "label_value_id": c.Data.ID},
			}}, user.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	draftID, _ := createProject(t, router, map[string]any{
		"name": "EN-FR Draft", "source_language": "en", "target_languages": []string{"fr"},
		"status": "draft",
	}, user.ID, tenant.ID)
	acme(draftID)
	createProject(t, router, map[string]any{
		"name": "EN-DE Active", "source_language": "en", "target_languages": []string{"de"},
		"status": "in_progress",
	}, user.ID, tenant.ID)
	doneID, _ := createProject(t, router, map[string]any{
		"name": "ES-FR Done", "source_language": "es", "target_languages": []string{"fr"},
		"status": "completed",
	}, user.ID, tenant.ID)
	acme(doneID)

	// Filter: status=draft → 1 result
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?status=draft", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page listResp
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Name != "EN-FR Draft" {
		t.Errorf("status=draft: got %+v", page.Data)
	}

	// Filter: customer_id → 2 results (the two carrying the Acme value)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects?customer_id=%d", c.Data.ID), nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 2 {
		t.Errorf("customer filter: got %d projects, want 2", len(page.Data))
	}

	// Filter: source=es → 1 result (ES-FR Done)
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?source=es", nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Name != "ES-FR Done" {
		t.Errorf("source=es: got %+v", page.Data)
	}

	// Filter: target=fr → 2 results (EN-FR Draft, ES-FR Done)
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?target=fr", nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 2 {
		t.Errorf("target=fr: got %d projects, want 2", len(page.Data))
	}

	// Filter: q=EN matches both EN-* projects (ILIKE)
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?q=EN", nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 2 {
		t.Errorf("q=EN: got %d projects, want 2", len(page.Data))
	}

	// Filter: created_by=user → all 3 (auto-populated), each carrying the
	// resolved creator name the list UI displays.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects?created_by=%d", user.ID), nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 3 {
		t.Errorf("created_by=me: got %d, want 3", len(page.Data))
	}
	for _, p := range page.Data {
		if p.CreatedByName != user.Name {
			t.Errorf("created_by_name for %q = %q, want %q", p.Name, p.CreatedByName, user.Name)
		}
	}

	// Filter: created_by=<somebody else> → nothing. Proves the picker can
	// narrow to a teammate, not just "me".
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects?created_by=%d", user.ID+9999), nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 0 {
		t.Errorf("created_by=<other user>: got %d, want 0", len(page.Data))
	}

	// Pagination: limit=2 → 2 + has_more=true
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?limit=2", nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 2 {
		t.Errorf("limit=2: got %d", len(page.Data))
	}
	if !page.HasMore {
		t.Error("limit=2 with 3 projects: expected has_more=true")
	}
	if page.Cursor == 0 {
		t.Error("expected non-zero cursor when has_more=true")
	}

	// Page 2: cursor=<last id from page 1>
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects?limit=2&cursor=%d", page.Cursor), nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 {
		t.Errorf("page 2: got %d, want 1", len(page.Data))
	}
	if page.HasMore {
		t.Error("page 2 with 1 result: expected has_more=false")
	}

	// Invalid status filter → 400
	rec = testutil.DoRequest(t, router, "GET", "/api/projects?status=garbage", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// The "Created by" chip's options come from this endpoint,
// not /api/members — a member who created nothing must not be offered as a
// filter that can only return an empty list. Also the usual tenant boundary.
func TestIntegrationProjectCreatorsEndpoint(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "CreatorsCorp")
	creator := testutil.SeedUser(t, q, tenant.ID, "creator@test.com", "pass")
	// A second member of the same org who never creates anything.
	bystander := testutil.SeedUserWithRole(t, q, tenant.ID, "bystander@test.com", "pass", "admin")

	other := testutil.SeedTenant(t, q, "OtherCorp")
	otherUser := testutil.SeedUser(t, q, other.ID, "other@test.com", "pass")

	mkProject := func(name string, userID, tenantID int64) {
		rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
			"name": name, "source_language": "en", "target_languages": []string{"fr"},
		}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
	}
	mkProject("Mine A", creator.ID, tenant.ID)
	mkProject("Mine B", creator.ID, tenant.ID)
	mkProject("Theirs", otherUser.ID, other.ID)

	type creatorRow struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	list := func(userID, tenantID int64) []creatorRow {
		rec := testutil.DoRequest(t, router, "GET", "/api/projects/creators", nil, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []creatorRow `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data
	}

	rows := list(creator.ID, tenant.ID)
	// Two projects by one person collapse to one option (DISTINCT), and the
	// bystander is absent even though they're a member of the org.
	if len(rows) != 1 {
		t.Fatalf("got %+v, want exactly 1 creator", rows)
	}
	if rows[0].ID != creator.ID {
		t.Errorf("creator id = %d, want %d", rows[0].ID, creator.ID)
	}
	if rows[0].Name != creator.Name {
		t.Errorf("creator name = %q, want %q", rows[0].Name, creator.Name)
	}
	for _, r := range rows {
		if r.ID == bystander.ID {
			t.Error("a member who created no project must not be offered as an option")
		}
	}

	// Tenant boundary: the other org sees only its own creator.
	otherRows := list(otherUser.ID, other.ID)
	if len(otherRows) != 1 || otherRows[0].ID != otherUser.ID {
		t.Errorf("cross-tenant leak: %+v", otherRows)
	}
}

// Pins that PATCH /api/projects/{id} is admin-only.
// Anti-tautology: moving the route out of the admin-only group in api.go
// turns this test RED.
func TestIntegrationProjectMetadataRoleGate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "member")

	pid, _ := createProject(t, router, map[string]any{
		"name": "P", "source_language": "en", "target_languages": []string{"fr"},
	}, owner.ID, tenant.ID)

	// Member cannot change status
	rec := testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"status": "completed"}, member.ID, tenant.ID, "member")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Member cannot rename either (project metadata is PM-territory)
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"name": "Renamed by member"}, member.ID, tenant.ID, "member")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Owner can do both
	rec = testutil.DoRequestWithRole(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]string{"status": "on_hold"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// Client + domain are optional taxonomy fields set at create time
// and editable via the metadata PATCH. Trim-empty → NULL on create; the
// PATCH treats absent as "keep" and empty-string as "clear" (COALESCE
// in queries.sql + handler pointer-vs-absent logic). Round-trip through GET.
func TestIntegrationProjectClientDomainLifecycle(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")

	type clientDomainResp struct {
		Data struct {
			ID     int64   `json:"id"`
			Client *string `json:"client"`
			Domain *string `json:"domain"`
		} `json:"data"`
	}

	// Create WITH client + domain
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Acme Patents",
		"source_language":  "en",
		"target_languages": []string{"de"},
		"client":           "Acme Legal",
		"domain":           "Patents",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var p clientDomainResp
	testutil.DecodeJSON(t, rec, &p)
	if p.Data.Client == nil || *p.Data.Client != "Acme Legal" {
		t.Errorf("create: client want %q, got %v", "Acme Legal", p.Data.Client)
	}
	if p.Data.Domain == nil || *p.Data.Domain != "Patents" {
		t.Errorf("create: domain want %q, got %v", "Patents", p.Data.Domain)
	}
	pid := p.Data.ID

	// Create WITHOUT client/domain → NULL in DB
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Plain",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var plain clientDomainResp
	testutil.DecodeJSON(t, rec, &plain)
	if plain.Data.Client != nil {
		t.Errorf("absent client should be NULL, got %v", plain.Data.Client)
	}

	// Create with whitespace-only client → server trims to "" → NULL
	rec = testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "WhitespaceClient",
		"source_language":  "en",
		"target_languages": []string{"fr"},
		"client":           "   ",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var ws clientDomainResp
	testutil.DecodeJSON(t, rec, &ws)
	if ws.Data.Client != nil {
		t.Errorf("whitespace client should trim to NULL, got %v", ws.Data.Client)
	}

	// PATCH: change domain only — client must be preserved (COALESCE absent → keep)
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"domain": "Pharmaceuticals"}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &p)
	if p.Data.Client == nil || *p.Data.Client != "Acme Legal" {
		t.Errorf("PATCH (domain only): client should be kept, got %v", p.Data.Client)
	}
	if p.Data.Domain == nil || *p.Data.Domain != "Pharmaceuticals" {
		t.Errorf("PATCH (domain only): domain should update, got %v", p.Data.Domain)
	}

	// PATCH: clear client by sending empty string (COALESCE pattern — "" sets
	// the column to "", which readers treat as no-value).
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d", pid),
		map[string]any{"client": ""}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &p)
	if p.Data.Client == nil || *p.Data.Client != "" {
		t.Errorf("PATCH client='' should persist empty string, got %v", p.Data.Client)
	}
}

// Cross-tenant isolation: tenant A's filtered list never returns tenant B's projects.
func TestIntegrationProjectFilterCrossTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenantA := testutil.SeedTenant(t, q, "A")
	tenantB := testutil.SeedTenant(t, q, "B")
	userA := testutil.SeedUserWithRole(t, q, tenantA.ID, "a@test.com", "pass", "owner")
	userB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b@test.com", "pass", "owner")

	createProject(t, router, map[string]any{
		"name": "A's Project", "source_language": "en", "target_languages": []string{"fr"},
	}, userA.ID, tenantA.ID)
	createProject(t, router, map[string]any{
		"name": "B's Project", "source_language": "en", "target_languages": []string{"fr"},
	}, userB.ID, tenantB.ID)

	// A's filtered list excludes B's project even though target lang matches
	rec := testutil.DoRequest(t, router, "GET", "/api/projects?target=fr", nil, userA.ID, tenantA.ID)
	var page listResp
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Name != "A's Project" {
		t.Errorf("cross-tenant leak: A's list got %+v", page.Data)
	}
}
