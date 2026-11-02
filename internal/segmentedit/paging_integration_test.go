package segmentedit_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"openloc/internal/testutil"
)

// The customer API pages by whole positions: a page never ends between the
// pieces of a split, and the cursor still means "start at this position".
func TestIntegrationSplit_SegmentPagesKeepPiecesTogether(t *testing.T) {
	f := newFixture(t)
	f.split(t, "de", f.trs["de"][0].ID, len("Press the button"))
	key := testutil.SeedAPIToken(t, f.q, f.tenantID, f.userID, "read", time.Hour)

	type page struct {
		Data []struct {
			Position    int32  `json:"position"`
			SubPosition int32  `json:"sub_position"`
			Number      string `json:"number"`
			SourceText  string `json:"source_text"`
		} `json:"data"`
		NextCursor int64 `json:"cursor"`
		HasMore    bool  `json:"has_more"`
	}
	get := func(cursor int) page {
		path := fmt.Sprintf("/api/v1/jobs/%d/segments?limit=1&cursor=%d", f.jobs["de"].ID, cursor)
		rec := testutil.DoRequestWithRawToken(t, f.router, http.MethodGet, path, nil, key)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	first := get(0)
	var numbers []string
	for _, s := range first.Data {
		numbers = append(numbers, s.Number)
	}
	if !slices.Equal(numbers, []string{"1", "1.1"}) || !first.HasMore || first.NextCursor != 1 {
		t.Fatalf("first page: numbers %v has_more %v next %d, want both pieces of 1, more from 1", numbers, first.HasMore, first.NextCursor)
	}
	second := get(int(first.NextCursor))
	if len(second.Data) != 1 || second.Data[0].Number != "2" || second.HasMore {
		t.Errorf("second page: %+v, want row 2 and nothing more", second)
	}
}
