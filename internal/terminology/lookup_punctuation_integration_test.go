package terminology_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A term that holds punctuation (R&D, Node.js, TCP/IP, C++, Inc.) is found in
// a segment that contains it word for word, wherever it sits in the sentence.
func TestIntegrationTermLookupFindsTermsWithPunctuation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "PunctCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "punct@test.com", "pass")
	ctx := context.Background()

	base, err := q.CreateTermBase(ctx, db.CreateTermBaseParams{TenantID: tenant.ID, Name: "Tech", SourceLanguage: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range [][2]string{
		{"R&D", "I+D"}, {"Node.js", "Node.js"}, {"TCP/IP", "TCP/IP"}, {"C++", "C++"},
		{"Acme Inc.", "Acme S.A."}, {"R&D department", "departamento de I+D"}, {"password", "contraseña"},
	} {
		mustCreateEntry(t, q, base, term[0], term[1], "es", "approved")
	}
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Punct Template")
	if _, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: tmpl.SettingsProfileID, TermBaseID: base.ID,
		SourceLanguage: "en", TargetLanguage: "es", Mode: "read", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}

	source := "Our R&D department (Acme Inc.) ships Node.js, C++ and TCP/IP tools."
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf(
		"/api/terminology/lookup?template_id=%d&source_language=en&target_language=es&source_text=%s",
		tmpl.ID, url.QueryEscape(source)), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			SourceTerm string `json:"source_term"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	var got []string
	for _, d := range resp.Data {
		got = append(got, d.SourceTerm)
	}
	sort.Strings(got)
	want := []string{"Acme Inc.", "C++", "Node.js", "R&D", "R&D department", "TCP/IP"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("found %v, want %v", got, want)
	}
}
