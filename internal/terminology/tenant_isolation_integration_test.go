package terminology_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The term base routes below look the base up by id and tenant before they
// read or write anything, so another tenant's base id is a 404 that changes
// nothing. Each route also runs for the base's own tenant, so the 404 comes
// from the tenant check and not from the request.
func TestIntegrationTermBaseRoutesCrossTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	victimUser, victim := testutil.RegisterUser(t, q, "victim")
	attackerUser, attacker := testutil.RegisterUser(t, q, "attacker")

	base := testutil.SeedTermBase(t, q, victim, "Victim base", "en", "es")
	mustCreateEntry(t, q, base, "hello", "hola", "es", "approved")
	basePath := fmt.Sprintf("/api/terminology/%d", base.ID)

	// termTexts lists every live term text in the victim's base.
	termTexts := func() []string {
		t.Helper()
		rows, err := q.ListConceptTermsForExport(ctx, base.ID)
		if err != nil {
			t.Fatalf("ListConceptTermsForExport: %v", err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Text)
		}
		slices.Sort(out)
		return out
	}

	t.Run("PATCH renames only the own tenant's base", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodPatch, basePath,
			map[string]string{"name": "Hijacked", "description": "owned"}, attackerUser, attacker)
		if got := getBaseName(t, q, base.ID, victim); got != "Victim base" {
			t.Errorf("attacker renamed the base to %q", got)
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodPatch, basePath,
			map[string]string{"name": "Renamed by owner"}, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if got := getBaseName(t, q, base.ID, victim); got != "Renamed by owner" {
			t.Fatalf("owner rename not stored, name = %q", got)
		}
	})

	t.Run("CSV import writes only into the own tenant's base", func(t *testing.T) {
		path := basePath + "/import?target=es"
		before := termTexts()
		rec := testutil.DoFileUpload(t, router, path, "terms.csv", "text/csv",
			"source_term,target_term,status\nstolen,robado,approved\n", attackerUser, attacker)
		if after := termTexts(); !slices.Equal(after, before) {
			t.Errorf("attacker import changed the base: %v → %v", before, after)
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoFileUpload(t, router, path, "terms.csv", "text/csv",
			"source_term,target_term,status\nworld,mundo,approved\n", victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if after := termTexts(); !slices.Contains(after, "mundo") {
			t.Fatalf("owner import not stored, terms = %v", after)
		}
	})

	t.Run("CSV export reads only the own tenant's base", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodGet, basePath+"/export?target=es", nil, attackerUser, attacker)
		if strings.Contains(rec.Body.String(), "hola") {
			t.Errorf("attacker export leaked a term: %s", rec.Body.String())
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodGet, basePath+"/export?target=es", nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "hello,hola") {
			t.Fatalf("owner export missing the term: %s", rec.Body.String())
		}
	})

	t.Run("languages reads only the own tenant's base", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodGet, basePath+"/languages", nil, attackerUser, attacker)
		if strings.Contains(rec.Body.String(), `"es"`) {
			t.Errorf("attacker read the base's languages: %s", rec.Body.String())
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoRequest(t, router, http.MethodGet, basePath+"/languages", nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []string `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if !slices.Equal(resp.Data, []string{"en", "es"}) {
			t.Fatalf("owner languages = %v, want [en es]", resp.Data)
		}
	})

	t.Run("TBX import writes only into the own tenant's base", func(t *testing.T) {
		path := basePath + "/import-tbx"
		before := termTexts()
		rec := testutil.DoFileUpload(t, router, path, "terms.tbx", "application/xml",
			tbxConcept("intruder", "intruso"), attackerUser, attacker)
		if after := termTexts(); !slices.Equal(after, before) {
			t.Errorf("attacker TBX import changed the base: %v → %v", before, after)
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		rec = testutil.DoFileUpload(t, router, path, "terms.tbx", "application/xml",
			tbxConcept("friend", "amigo"), victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if after := termTexts(); !slices.Contains(after, "amigo") {
			t.Fatalf("owner TBX import not stored, terms = %v", after)
		}
	})
}

func getBaseName(t *testing.T, q db.Querier, id, tenantID int64) string {
	t.Helper()
	b, err := q.GetTermBase(testutil.Ctx(), db.GetTermBaseParams{ID: id, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetTermBase: %v", err)
	}
	return b.Name
}

// tbxConcept is a one-concept TBX-Basic v3 file with an en and an es term.
func tbxConcept(en, es string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
  <text><body>
    <conceptEntry id="c1">
      <langSec xml:lang="en"><termSec><term>` + en + `</term></termSec></langSec>
      <langSec xml:lang="es"><termSec><term>` + es + `</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`
}
