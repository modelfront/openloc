package segmentedit_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

// A join checks every row it names, not only the first: a locked, held or
// pre-translated second row refuses it with that row's code, and the job's
// rows stay as they were.
func TestIntegrationJoin_RowRefusals(t *testing.T) {
	f := newSentenceFixture(t)
	de := f.trs["de"]
	other := testutil.SeedUserWithRole(t, f.q, f.tenantID, "other@test.com", "pass", "project_manager")
	before := f.rows(t, "de")

	cases := []struct {
		name   string
		setup  func(t *testing.T) (undo func())
		status int
		code   string
	}{
		{"second row locked", func(t *testing.T) func() {
			f.exec(t, `UPDATE translations SET locked = true WHERE id = $1`, de[1].ID)
			return func() { f.exec(t, `UPDATE translations SET locked = false WHERE id = $1`, de[1].ID) }
		}, http.StatusConflict, "segment_locked"},
		{"second row held by another user", func(t *testing.T) func() {
			testutil.SeedHold(t, f.q, f.tenantID, f.jobs["de"].ID, de[1].ID, other.ID)
			return func() { f.exec(t, `DELETE FROM translation_holds WHERE translation_id = $1`, de[1].ID) }
		}, http.StatusConflict, "segment_held"},
		{"second row being pre-translated", func(t *testing.T) func() {
			f.exec(t, `UPDATE translations SET in_flight_phase = 'mt' WHERE id = $1`, de[1].ID)
			return func() { f.exec(t, `UPDATE translations SET in_flight_phase = NULL WHERE id = $1`, de[1].ID) }
		}, http.StatusConflict, "segment_in_flight"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer c.setup(t)()
			rec := f.join(t, "de", de[0].ID, de[1].ID)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.code) {
				t.Errorf("%d %s, want %d %s", rec.Code, rec.Body.String(), c.status, c.code)
			}
			if after := f.rows(t, "de"); !reflect.DeepEqual(after, before) {
				t.Errorf("the German job's rows changed after a refusal: %+v, want %+v", after, before)
			}
		})
	}
}

// A unit whose <mrk> sentences have an element between them in <seg-source>
// cannot merge them, so the join is refused before anything is written.
const unsafeXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">
  <file original="a.txt" datatype="plaintext" source-language="en" target-language="de">
    <body>
      <trans-unit id="a1">
        <source>One. Two.</source>
        <seg-source><mrk mtype="seg" mid="1">One.</mrk><x id="9"/><mrk mtype="seg" mid="2">Two.</mrk></seg-source>
        <target><mrk mtype="seg" mid="1">Eins.</mrk><mrk mtype="seg" mid="2">Zwei.</mrk></target>
      </trans-unit>
    </body>
  </file>
</xliff>`

func TestIntegrationJoin_UnsafeXLIFFRun(t *testing.T) {
	f := newFileFixture(t, unsafeXLIFF, []fileSegment{{"a1:1", "One."}, {"a1:2", "Two."}},
		map[string][]string{"de": {"Eins.", "Zwei."}})
	f.exec(t, `UPDATE files SET format = 'xliff' WHERE id = $1`, f.file.ID)
	before := f.rows(t, "de")

	rec := f.join(t, "de", f.trs["de"][0].ID, f.trs["de"][1].ID)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "join_unsafe") {
		t.Errorf("%d %s, want 400 join_unsafe", rec.Code, rec.Body.String())
	}
	if after := f.rows(t, "de"); !reflect.DeepEqual(after, before) {
		t.Errorf("the German job's rows changed after a refusal: %+v, want %+v", after, before)
	}
}

// jobRows is a job's rows as the editor lists them, without their state.
type jobRows struct {
	segments, translations []int64
	positions, subs        []int32
}

func (f *fixture) rows(t *testing.T, lang string) jobRows {
	t.Helper()
	idx := f.index(t, lang)
	return jobRows{idx.SegmentIDs, idx.TranslationIDs, idx.Positions, idx.SubPositions}
}
