package airouter

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// MatchRule picks the rule for a job's target language: exact normalized
// match first, then same base language, first listed wins among base matches.
func TestMatchRule(t *testing.T) {
	rule := func(engine int64, lang string) db.AiRouterRule {
		return db.AiRouterRule{EngineID: engine, TargetLang: lang}
	}
	cases := []struct {
		name   string
		rules  []db.AiRouterRule
		target string
		want   int64 // 0 = nil
	}{
		{"exact", []db.AiRouterRule{rule(1, "fr"), rule(2, "de")}, "de", 2},
		{"no match", []db.AiRouterRule{rule(1, "fr")}, "es", 0},
		{"normalizes the job code", []db.AiRouterRule{rule(1, "pt-br")}, "PT_BR", 1},
		{"normalizes the rule code", []db.AiRouterRule{rule(1, "PT-BR")}, "pt-br", 1},
		{"base rule covers a regional job", []db.AiRouterRule{rule(1, "fr")}, "fr-ca", 1},
		{"regional rule covers a base job", []db.AiRouterRule{rule(1, "fr-fr")}, "fr", 1},
		{"exact beats base regardless of order", []db.AiRouterRule{rule(1, "fr"), rule(2, "fr-ca")}, "fr-ca", 2},
		{"first base match wins", []db.AiRouterRule{rule(1, "fr-fr"), rule(2, "fr-be")}, "fr-ca", 1},
		{"different base does not match", []db.AiRouterRule{rule(1, "en")}, "es", 0},
		{"empty target matches nothing", []db.AiRouterRule{rule(1, "en")}, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchRule(c.rules, c.target)
			switch {
			case c.want == 0 && got != nil:
				t.Fatalf("want no match, got engine %d", *got)
			case c.want != 0 && (got == nil || *got != c.want):
				t.Fatalf("want engine %d, got %v", c.want, got)
			}
		})
	}
}
