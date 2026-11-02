package publicapi_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// Split and join through the customer API, end to end; the client checks
// every response against the spec.
func TestIntegrationV1_SplitAndJoin(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "SplitJoin")
	c := f.client
	base := f.path("/jobs/{jobID}/segments")
	key := f.key(t, "linguist")

	rec := c.do(http.MethodPost, fmt.Sprintf("%s/%d/split", base, f.ids["translationID"]), map[string]any{"offset": len("Hello")}, key)
	wantStatus(t, rec, http.StatusOK)
	split := testutil.DecodeData[publicapi.SegmentEditResult](t, rec)
	if len(split.SegmentIDs) != 2 || split.SegmentIDs[0] != f.ids["translationID"] {
		t.Fatalf("split = %+v, want the segment and a new second piece", split)
	}

	rec = c.do(http.MethodGet, base, nil, key)
	wantStatus(t, rec, http.StatusOK)
	segs := testutil.DecodeData[[]publicapi.Segment](t, rec)
	var numbers, sources []string
	for _, s := range segs {
		numbers, sources = append(numbers, s.Number), append(sources, s.SourceText)
	}
	if !slices.Equal(numbers, []string{"1", "1.1"}) || !slices.Equal(sources, []string{"Hello", "world."}) {
		t.Fatalf("after the split: numbers %v sources %v", numbers, sources)
	}

	// A version read before the split is stale for the join.
	rec = c.do(http.MethodPost, base+"/join", map[string]any{"segments": []map[string]any{
		{"id": segs[0].ID, "expected_updated_at": "2020-01-01T00:00:00Z"}, {"id": segs[1].ID},
	}}, key)
	wantStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "conflict" {
		t.Fatalf("stale join code = %q, want conflict", code)
	}

	rec = c.do(http.MethodPost, base+"/join", map[string]any{"segments": []map[string]any{
		{"id": segs[0].ID, "expected_updated_at": segs[0].UpdatedAt}, {"id": segs[1].ID, "expected_updated_at": segs[1].UpdatedAt},
	}}, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.SegmentEditResult](t, rec); !slices.Equal(got.SegmentIDs, []int64{f.ids["translationID"]}) {
		t.Fatalf("join = %+v", got)
	}

	rec = c.do(http.MethodGet, base, nil, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.Segment](t, rec); len(got) != 1 || got[0].SourceText != "Hello world." || got[0].Number != "1" {
		t.Fatalf("after the join: %+v, want the segment back", got)
	}
}
