package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// An entry Find & Replace changed records its editor as "Name <email>", like
// every other TM write, so the editor's match card can show the name — and
// strip the email for a linguist — instead of a bare address.
func TestIntegrationTMFindReplace_RecordsUserLabel(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LabelCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "fiona@test.com", "pass")
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Fiona Replace' WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	tm := testutil.SeedTM(t, q, tenant.ID, "Label TM")
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Open the file", TargetText: "Datei öffnen", CreatedBy: "seed",
	}); err != nil {
		t.Fatal(err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{"find": "Datei", "replace": "Dokument", "mode": "replace_all", "scope": "target"},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	entries := tmEntries(t, q, tm.ID)
	if len(entries) != 1 || entries[0].TargetText != "Dokument öffnen" {
		t.Fatalf("entries = %+v", entries)
	}
	if got, want := entries[0].UpdatedBy, "Fiona Replace <fiona@test.com>"; got != want {
		t.Errorf("updated_by = %q, want %q", got, want)
	}
}
