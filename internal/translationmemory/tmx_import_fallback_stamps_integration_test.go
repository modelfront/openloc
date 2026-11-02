package translationmemory_test

import (
	"net/http"
	"testing"
	"time"

	"openloc/internal/testutil"
)

// When the multi-row INSERT refuses a batch, the entries go in one by one —
// and keep the TU's creation date, change date and change user exactly as the
// multi-row path stores them, so an entry's stamps on the editor's match card
// do not depend on which path imported it.
func TestIntegrationTMXImport_FallbackKeepsTUStamps(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMXFallback")
	user := testutil.SeedUser(t, q, tenant.ID, "tmx-fallback@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Fallback TM")

	// Two "human" TUs with the same pair break the pipeline's unique index,
	// so the batch falls back: one of them is skipped, the stamped TU is kept.
	const dup = `<tu creationid="human"><tuv xml:lang="en"><seg>Same</seg></tuv><tuv xml:lang="es"><seg>Igual</seg></tuv></tu>`
	body := `<?xml version="1.0" encoding="UTF-8"?><tmx version="1.4"><header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="t" creationtoolversion="1" o-tmf="t"/><body>` +
		dup + dup +
		`<tu creationid="translator01" creationdate="20250101T100000Z" changeid="reviewer02" changedate="20250601T120000Z"><tuv xml:lang="en"><seg>Stamped</seg></tuv><tuv xml:lang="es"><seg>Sellado</seg></tuv></tu>` +
		`</body></tmx>`
	resp, imp := uploadTMX(t, router, tm.ID, body, user.ID, tenant.ID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status %d, want 202", resp.StatusCode)
	}
	done := waitImportDone(t, q, imp.ID)
	if done.Imported != 2 || done.Skipped != 1 {
		t.Fatalf("import = %+v, want 2 imported and 1 skipped (the batch must have fallen back)", done)
	}

	var stamped *struct {
		createdAt, updatedAt time.Time
		createdBy, updatedBy string
	}
	for _, e := range tmEntries(t, q, tm.ID) {
		if e.SourceText == "Stamped" {
			stamped = &struct {
				createdAt, updatedAt time.Time
				createdBy, updatedBy string
			}{e.CreatedAt.Time, e.UpdatedAt.Time, e.CreatedBy, e.UpdatedBy}
		}
	}
	if stamped == nil {
		t.Fatal("the stamped TU was not imported")
	}
	if want := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC); !stamped.createdAt.Equal(want) {
		t.Errorf("created_at = %v, want %v", stamped.createdAt, want)
	}
	if want := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC); !stamped.updatedAt.Equal(want) {
		t.Errorf("updated_at = %v, want %v", stamped.updatedAt, want)
	}
	if stamped.createdBy != "translator01" || stamped.updatedBy != "reviewer02" {
		t.Errorf("users = created %q, updated %q; want translator01, reviewer02", stamped.createdBy, stamped.updatedBy)
	}
}
