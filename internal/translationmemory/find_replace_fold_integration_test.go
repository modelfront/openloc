package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Case-insensitive replace cuts the stored text where the match is, even when
// a letter before it changes byte length in lower case (Turkish İ is 2 bytes,
// its lower case "i̇" 3).
func TestIntegrationTMFindReplace_CaseInsensitiveKeepsNonASCIIText(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FoldCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "fold@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Fold TM")
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "tr",
		SourceText: "Istanbul and Ankara", TargetText: "İstanbul ve İzmir ve ANKARA", CreatedBy: "seed",
	}); err != nil {
		t.Fatal(err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{"find": "ankara", "replace": "Bursa", "mode": "replace_all", "scope": "target"},
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	entries := tmEntries(t, q, tm.ID)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if got, want := entries[0].TargetText, "İstanbul ve İzmir ve Bursa"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}
