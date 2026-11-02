package segmentedit_test

import (
	"context"
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// The TM's 101% context reads a row's neighbours in its own job: after a
// German split, a German piece's neighbours are the German pieces, while the
// French job, which kept the imported rows, still sees those.
func TestIntegrationNeighbours_FollowTheJobsRows(t *testing.T) {
	f := newFixture(t)
	f.split(t, "de", f.trs["de"][0].ID, len("Press the button"))
	ctx := context.Background()
	de := f.index(t, "de").SegmentIDs
	if len(de) != 3 {
		t.Fatalf("German rows after the split: %d, want 3", len(de))
	}
	secondPiece, shared := de[1], f.segs[0].ID

	got, err := f.q.GetSegmentNeighborSources(ctx, db.GetSegmentNeighborSourcesParams{TargetLanguage: "de", SegmentID: secondPiece})
	if err != nil {
		t.Fatal(err)
	}
	if got.PrevSource != "Press the button" || got.NextSource != "Next one" {
		t.Errorf("German piece's neighbours = %q / %q, want the first piece / the next line", got.PrevSource, got.NextSource)
	}
	got, err = f.q.GetSegmentNeighborSources(ctx, db.GetSegmentNeighborSourcesParams{TargetLanguage: "fr", SegmentID: shared})
	if err != nil {
		t.Fatal(err)
	}
	if got.PrevSource != "" || got.NextSource != "Next one" {
		t.Errorf("French row's neighbours = %q / %q, want none / the next line", got.PrevSource, got.NextSource)
	}

	byID, err := f.q.ListSegmentNeighborSourcesByIDs(ctx, db.ListSegmentNeighborSourcesByIDsParams{
		SegmentIds: []int64{secondPiece, shared}, TargetLanguages: []string{"de", "fr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][2]string{secondPiece: {"Press the button", "Next one"}, shared: {"", "Next one"}}
	if len(byID) != 2 {
		t.Fatalf("neighbours by id: %d rows, want 2", len(byID))
	}
	for _, r := range byID {
		if w := want[r.ID]; r.PrevSource != w[0] || r.NextSource != w[1] {
			t.Errorf("segment %d neighbours = %q / %q, want %q / %q", r.ID, r.PrevSource, r.NextSource, w[0], w[1])
		}
	}

	jobSources := func(lang string) []string {
		rows, err := f.q.ListSegmentSourcesByFile(ctx, db.ListSegmentSourcesByFileParams{TargetLanguage: lang, FileID: f.file.ID})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.SourceText
		}
		return out
	}
	if got := jobSources("de"); !slices.Equal(got, []string{"Press the button", "and wait for the light", "Next one"}) {
		t.Errorf("German sources = %q", got)
	}
	if got := jobSources("fr"); !slices.Equal(got, sources) {
		t.Errorf("French sources = %q, want the imported rows", got)
	}
}
