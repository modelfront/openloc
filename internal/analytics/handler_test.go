package analytics

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func TestDefaultWeights(t *testing.T) {
	// Verify all bands have defaults
	for _, band := range AllBands {
		if _, ok := DefaultWeights[band]; !ok {
			t.Errorf("missing default weight for band %q", band)
		}
	}
}

func TestGetWeightsMap_Defaults(t *testing.T) {
	// No stored rows → all defaults
	m := GetWeightsMap(nil)
	if m["tm_exact"] != 0 {
		t.Errorf("expected tm_exact=0, got %d", m["tm_exact"])
	}
	if m["new"] != 100 {
		t.Errorf("expected new=100, got %d", m["new"])
	}
	if m["tm_fuzzy_95"] != 25 {
		t.Errorf("expected tm_fuzzy_95=25, got %d", m["tm_fuzzy_95"])
	}
}

func TestGetWeightsMap_CustomOverrides(t *testing.T) {
	rows := []db.AnalysisWeight{
		{Band: "tm_exact", WeightPct: 10},
		{Band: "new", WeightPct: 80},
	}
	m := GetWeightsMap(rows)
	if m["tm_exact"] != 10 {
		t.Errorf("expected tm_exact=10, got %d", m["tm_exact"])
	}
	if m["new"] != 80 {
		t.Errorf("expected new=80, got %d", m["new"])
	}
	// Unchanged bands keep defaults
	if m["tm_fuzzy_85"] != 50 {
		t.Errorf("expected tm_fuzzy_85=50 (default), got %d", m["tm_fuzzy_85"])
	}
}

func TestMergeWeights(t *testing.T) {
	rows := []db.AnalysisWeight{
		{Band: "tm_exact", WeightPct: 5},
	}
	result := mergeWeights(rows)

	// Should have all bands in canonical order
	if len(result) != len(AllBands) {
		t.Fatalf("expected %d bands, got %d", len(AllBands), len(result))
	}
	// First band should be tm_exact with custom value
	if result[0].Band != "tm_exact" || result[0].WeightPct != 5 {
		t.Errorf("expected tm_exact=5, got %s=%d", result[0].Band, result[0].WeightPct)
	}
	// Second band should be tm_fuzzy_95 with default
	if result[1].Band != "tm_fuzzy_95" || result[1].WeightPct != 25 {
		t.Errorf("expected tm_fuzzy_95=25 (default), got %s=%d", result[1].Band, result[1].WeightPct)
	}
}

func TestWordsPerHour(t *testing.T) {
	tests := []struct {
		name    string
		words   int32
		timeMs  int64
		wantMin float64
		wantMax float64
	}{
		{"zero time", 100, 0, 0, 0},
		{"very short time", 10, 100, 0, 0}, // < 0.001 hours
		{"one hour", 500, 3_600_000, 499, 501},
		{"half hour", 250, 1_800_000, 499, 501},
		{"five minutes", 50, 300_000, 599, 601},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wordsPerHour(tt.words, tt.timeMs)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("wordsPerHour(%d, %d) = %.1f, want [%.1f, %.1f]",
					tt.words, tt.timeMs, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}
