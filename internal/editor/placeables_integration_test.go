package editor_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/editor"
	"openloc/internal/testutil"
)

func (f *fixture) rowsOf(t *testing.T, segIDs ...int64) []editor.Row {
	t.Helper()
	ids := ""
	for i, id := range segIDs {
		if i > 0 {
			ids += ","
		}
		ids += fmt.Sprint(id)
	}
	return getData[[]editor.Row](t, f, "rows?segment_ids="+ids)
}

func placeablesOf(r editor.Row) string {
	out := ""
	for _, p := range r.Placeables {
		out += string(p.Kind) + ":" + p.Text + " "
	}
	return out
}

func TestIntegrationEditor_RowsCarryPlaceables(t *testing.T) {
	f := newFixture(t)
	seg := f.segs[3]
	f.exec(t, `UPDATE segments SET source_text = 'Ticket TCK-1234: hello {name}, see https://example.com' WHERE id = $1`, seg.ID)

	rows := f.rowsOf(t, seg.ID)
	if got, want := placeablesOf(rows[0]), "code:TCK-1234 variable:{name} url:https://example.com "; got != want {
		t.Errorf("placeables %q, want %q", got, want)
	}

	// A row without placeables sends an empty list, not null.
	rec := testutil.DoRequest(t, f.router, http.MethodGet, fmt.Sprintf("/api/jobs/%d/editor/rows?segment_ids=%d", f.job.ID, f.segs[0].ID), nil, f.userID, f.tenantID)
	if body := rec.Body.String(); !strings.Contains(body, `"placeables":[]`) {
		t.Errorf("row without placeables: %s", body)
	}
}

func TestIntegrationEditor_RowsOfOtherTenantsJobNotFound(t *testing.T) {
	f := newFixture(t)
	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "editor-placeables-stranger")
	rec := testutil.DoRequest(t, f.router, http.MethodGet, fmt.Sprintf("/api/jobs/%d/editor/rows?segment_ids=%d", f.job.ID, f.segs[0].ID), nil, otherUser, otherTenant)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}
