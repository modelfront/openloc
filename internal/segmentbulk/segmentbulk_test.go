package segmentbulk

import (
	"reflect"
	"testing"
)

func TestNormalizeIDs(t *testing.T) {
	got, err := NormalizeIDs([]int64{3, 1, 3, 2, 1})
	if err != nil || !reflect.DeepEqual(got, []int64{3, 1, 2}) {
		t.Fatalf("NormalizeIDs = %v, %v; want [3 1 2]", got, err)
	}
	tooMany := make([]int64, MaxIDs+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for name, ids := range map[string][]int64{"empty": nil, "zero": {1, 0}, "negative": {-4}, "too many": tooMany} {
		if _, err := NormalizeIDs(ids); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// Duplicates count once against the cap.
	dup := append(make([]int64, 0, MaxIDs+1), tooMany[:MaxIDs]...)
	dup = append(dup, 1)
	if _, err := NormalizeIDs(dup); err != nil {
		t.Errorf("%d unique ids with a duplicate: %v", MaxIDs, err)
	}
}

func TestSplit_EveryIDOnceInRequestOrder(t *testing.T) {
	rows := map[int64]Row{1: {ID: 1}, 2: {ID: 2, Locked: true}, 3: {ID: 3}}
	res := Split([]int64{3, 9, 2, 1}, []int64{1, 3}, rows, func(r Row) SkipReason {
		if r.Locked {
			return Locked
		}
		return Unchanged
	})
	if !reflect.DeepEqual(res.Applied, []int64{3, 1}) {
		t.Errorf("applied = %v", res.Applied)
	}
	want := []Skip{{ID: 9, Reason: NotFound}, {ID: 2, Reason: Locked}}
	if !reflect.DeepEqual(res.Skipped, want) {
		t.Errorf("skipped = %v, want %v", res.Skipped, want)
	}
}
