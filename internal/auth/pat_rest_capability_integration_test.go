// An access key's capability holds on the REST API, not only on /mcp: a
// read-only key reads, and no key can create another key.
package auth_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func assertReadOnlyKey(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusForbidden || !strings.Contains(body, `"read_only_key"`) {
		t.Fatalf("status = %d body = %s, want 403 read_only_key", status, body)
	}
}

func TestIntegrationReadOnlyPATCannotWriteOverREST(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "ReadKeyOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-readkey@test.com", "pass", "owner")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling-readkey@test.com", "pass", "linguist")
	project := testutil.SeedProject(t, q, tenant.ID, "Read key target", "en", []string{"de"})
	readKey := testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityRead, time.Hour)

	rec := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, readKey)
	testutil.AssertStatus(t, rec, http.StatusOK)

	projectPath := "/api/projects/" + itoa(project.ID)
	rec = testutil.DoRequestWithRawToken(t, router, "DELETE", projectPath, nil, readKey)
	assertReadOnlyKey(t, rec.Code, rec.Body.String())
	rec = testutil.DoRequestWithRole(t, router, "GET", projectPath, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRawToken(t, router, "PUT", "/api/members/"+itoa(linguist.ID)+"/role",
		map[string]any{"role": "admin"}, readKey)
	assertReadOnlyKey(t, rec.Code, rec.Body.String())
	m, err := q.GetMembership(t.Context(), db.GetMembershipParams{UserID: linguist.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("membership role: %v", err)
	}
	if m.Role != "linguist" {
		t.Fatalf("role = %q after a read-only key's PUT, want linguist", m.Role)
	}

	rec = testutil.DoRequestWithRawToken(t, router, "POST", "/api/api-tokens",
		map[string]any{"name": "minted", "capability": "write"}, readKey)
	assertReadOnlyKey(t, rec.Code, rec.Body.String())

	rec = testutil.DoRequestWithRawToken(t, router, "POST", "/api/chat",
		map[string]any{"message": "delete everything", "allow_writes": true}, readKey)
	assertReadOnlyKey(t, rec.Code, rec.Body.String())
	rec = testutil.DoRequestWithRawToken(t, router, "POST", "/api/chat/confirm",
		map[string]any{"conversation_id": 1}, readKey)
	assertReadOnlyKey(t, rec.Code, rec.Body.String())

	tokens, err := q.ListAPITokensByTenant(t.Context(), tenant.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("owner has %d keys, want only the seeded one", len(tokens))
	}
}

func TestIntegrationWritePATWritesButCannotMintKeys(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "WriteKeyOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-writekey@test.com", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, "Write key target", "en", []string{"de"})
	writeKey := testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityWrite, time.Hour)

	projectPath := "/api/projects/" + itoa(project.ID)
	rec := testutil.DoRequestWithRawToken(t, router, "DELETE", projectPath, nil, writeKey)
	if rec.Code >= 300 {
		t.Fatalf("write key DELETE project: status %d body %s", rec.Code, rec.Body.String())
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", projectPath, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	rec = testutil.DoRequestWithRawToken(t, router, "POST", "/api/api-tokens",
		map[string]any{"name": "minted", "capability": "write"}, writeKey)
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// A browser session still creates keys.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/api-tokens",
		map[string]any{"name": "from-browser"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
}
