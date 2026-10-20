package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/tmx"
)

// A TMX export writes the entry's inline tags, so a tagged entry imported from
// a TMX file goes out with the same <bpt>/<ept> markup, and an entry without
// tags goes out as escaped plain text.
func TestIntegrationTMExportKeepsInlineTags(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ExportRich")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "tmx-export@test.com", "pass", "owner")
	tm := testutil.SeedTM(t, q, tenant.ID, "Export TM")

	srcRich := `Click <bpt i="1">&lt;b&gt;</bpt>here<ept i="1">&lt;/b&gt;</ept>`
	tgtRich := `Haz clic <bpt i="1">&lt;b&gt;</bpt>aquí<ept i="1">&lt;/b&gt;</ept>`
	for _, p := range []db.InsertTMParams{
		{TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Click here", TargetText: "Haz clic aquí",
			CreatedBy: "TMXImporter", SourceRichText: srcRich, TargetRichText: tgtRich},
		{TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "R&D", TargetText: "I+D", CreatedBy: "TMXImporter"},
	} {
		if _, err := q.InsertTM(ctx, p); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tms/%d/export", tm.ID), nil)
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(owner.ID, tenant.ID, "owner"))
	rec := httptest.NewRecorder()
	testutil.NewTestRouter(pool).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"<seg>" + srcRich + "</seg>", "<seg>" + tgtRich + "</seg>", "<seg>R&amp;D</seg>"} {
		if !strings.Contains(body, want) {
			t.Errorf("export lacks %s:\n%s", want, body)
		}
	}

	// The exported file imports back with the tags in the rich text.
	entries, err := tmx.ParseEntries(strings.NewReader(body))
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.SourceText == "Click here" {
			found = e.SourceRichText == srcRich && e.TargetRichText == tgtRich
		}
	}
	if !found {
		t.Errorf("re-imported entries lost the tags: %+v", entries)
	}
}
