package pipeline

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// matchAIEngineRow routes a source→target pair to the right AI engine row,
// honouring the "*" wildcard and preferring the most specific match. IDs stand
// in for whole rows so the assertions read clearly.
func TestMatchAIEngineRow(t *testing.T) {
	row := func(id int64, src string, tgts ...string) db.AiEngineRow {
		return db.AiEngineRow{ID: id, SourceLang: src, TargetLangs: tgts}
	}

	cases := []struct {
		name     string
		rows     []db.AiEngineRow
		src, tgt string
		wantID   int64 // 0 = expect no match
	}{
		{"exact pair", []db.AiEngineRow{row(1, "en", "fr", "de")}, "en", "fr", 1},
		{"no match", []db.AiEngineRow{row(1, "en", "fr")}, "en", "es", 0},
		{"wildcard source matches any source", []db.AiEngineRow{row(1, "*", "fr")}, "de", "fr", 1},
		{"wildcard target matches any target", []db.AiEngineRow{row(1, "en", "*")}, "en", "ja", 1},
		{"catch-all matches everything", []db.AiEngineRow{row(1, "*", "*")}, "zh", "ko", 1},
		{"normalizes before matching", []db.AiEngineRow{row(1, "EN", "FR-FR")}, "en", "fr-fr", 1},

		// Specificity: a specific row beats a wildcard regardless of order.
		{"specific source wins over wildcard source",
			[]db.AiEngineRow{row(1, "*", "fr"), row(2, "en", "fr")}, "en", "fr", 2},
		{"specific source wins even when wildcard is listed first",
			[]db.AiEngineRow{row(1, "*", "*"), row(2, "en", "fr")}, "en", "fr", 2},
		{"specific target beats wildcard target on same source",
			[]db.AiEngineRow{row(1, "en", "*"), row(2, "en", "fr")}, "en", "fr", 2},
		{"falls back to catch-all when no specific row matches",
			[]db.AiEngineRow{row(1, "en", "fr"), row(2, "*", "*")}, "ru", "pl", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchAIEngineRow(c.rows, c.src, c.tgt)
			if c.wantID == 0 {
				if got != nil {
					t.Fatalf("expected no match, got row %d", got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected row %d, got no match", c.wantID)
			}
			if got.ID != c.wantID {
				t.Fatalf("expected row %d, got row %d", c.wantID, got.ID)
			}
		})
	}
}
