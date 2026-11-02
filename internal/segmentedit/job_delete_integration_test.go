package segmentedit_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"openloc/internal/testutil"
)

// Deleting a job removes the pieces its splits made and nothing else: the
// file's other language keeps its own pieces and rows, and the shared rows
// stay for it.
func TestIntegrationJobDelete_RemovesOnlyItsLanguageRows(t *testing.T) {
	f := newFixture(t)
	f.split(t, "de", f.trs["de"][0].ID, len("Press the button"))
	f.split(t, "fr", f.trs["fr"][0].ID, len("Press the button"))
	frBefore := f.rows(t, "fr")
	sharedBefore := f.countSegments(t, "target_language IS NULL")

	path := fmt.Sprintf("/api/jobs/%d", f.jobs["de"].ID)
	if rec := testutil.DoRequest(t, f.router, http.MethodDelete, path, nil, f.userID, f.tenantID); rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("delete job: %d %s", rec.Code, rec.Body.String())
	}

	if n := f.countSegments(t, "target_language = 'de'"); n != 0 {
		t.Errorf("%d German pieces left after the German job's delete, want 0", n)
	}
	if n := f.countSegments(t, "target_language = 'fr'"); n != 2 {
		t.Errorf("%d French pieces after the German job's delete, want 2", n)
	}
	if n := f.countSegments(t, "target_language IS NULL"); n != sharedBefore {
		t.Errorf("%d shared rows after the German job's delete, want %d", n, sharedBefore)
	}
	if after := f.rows(t, "fr"); !reflect.DeepEqual(after, frBefore) {
		t.Errorf("the French job's rows changed: %+v, want %+v", after, frBefore)
	}
}

// countSegments counts the file's segments matching where.
func (f *fixture) countSegments(t *testing.T, where string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM segments WHERE file_id = $1 AND `+where, f.file.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
