package aiprompt

import (
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// addition builds a target-only addition, the common shape.
func addition(id int64, pos int32, text string, targetLangs ...string) db.AiPromptLanguageAddition {
	return db.AiPromptLanguageAddition{ID: id, Position: pos, AdditionText: text, TargetLanguages: targetLangs}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name     string
		tgtCodes []string
		jobTgt   string
		want     bool
	}{
		{"matches on the target", []string{"de"}, "de", true},
		{"rejects another target", []string{"de"}, "fr", false},
		{"any code in the list matches", []string{"fr", "de", "it"}, "de", true},

		// Base-subtag matching: an addition written for a language covers its
		// regional variants, the same way TM and terminology assignments do.
		{"addition for de covers a de-AT job", []string{"de"}, "de-at", true},
		{"addition for de-AT covers a de job", []string{"de-at"}, "de", true},
		{"two regions of one language match", []string{"pt-br"}, "pt-pt", true},

		// The codes are normalized on write, but a job's languages come from
		// the project row and may not be.
		{"un-normalized job language still matches", []string{"de"}, "DE_AT", true},
		{"un-normalized code still matches", []string{"DE_AT"}, "de", true},

		// No language is an unfinished row, not a wildcard — otherwise
		// unreviewed text would ride on every request.
		{"no language matches nothing", nil, "de", false},
		{"blank codes match nothing", []string{"", "  "}, "de", false},
		// …and an empty job language must not collide with a blank code.
		{"empty job language matches nothing", []string{"de"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Matches(tt.tgtCodes, tt.jobTgt)
			if got != tt.want {
				t.Errorf("Matches(tgt=%v, job into %q) = %v, want %v",
					tt.tgtCodes, tt.jobTgt, got, tt.want)
			}
		})
	}
}

func TestApplyLanguageAdditions(t *testing.T) {
	base := "You are a reviewer."

	t.Run("a job with no matching addition gets the text unchanged", func(t *testing.T) {
		adds := []db.AiPromptLanguageAddition{addition(1, 0, "Use Sie.", "de")}
		if got := ApplyLanguageAdditions(base, adds, "fr"); got != base {
			t.Errorf("got %q, want the base text unchanged", got)
		}
	})

	t.Run("a matching addition is appended after the instruction", func(t *testing.T) {
		adds := []db.AiPromptLanguageAddition{addition(1, 0, "Use Sie.", "de")}
		got := ApplyLanguageAdditions(base, adds, "de")
		if !strings.HasPrefix(got, base) {
			t.Errorf("the tenant's own text must come first, got %q", got)
		}
		if !strings.HasSuffix(got, "Use Sie.") {
			t.Errorf("the addition must be appended, got %q", got)
		}
	})

	t.Run("several matching additions keep position order", func(t *testing.T) {
		// Deliberately supplied out of order: the execution path must not
		// depend on the caller having sorted them.
		adds := []db.AiPromptLanguageAddition{
			addition(9, 2, "third", "de"),
			addition(3, 0, "first", "de"),
			addition(7, 1, "second", "de"),
		}
		got := ApplyLanguageAdditions(base, adds, "de")
		if want := base + "\n\nfirst\n\nsecond\n\nthird"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("only the matching ones are appended", func(t *testing.T) {
		adds := []db.AiPromptLanguageAddition{
			addition(1, 0, "german advice", "de"),
			addition(2, 1, "french advice", "fr"),
		}
		got := ApplyLanguageAdditions(base, adds, "de")
		if !strings.Contains(got, "german advice") {
			t.Error("the matching addition is missing")
		}
		if strings.Contains(got, "french advice") {
			t.Error("a non-matching addition must not be sent")
		}
	})

	t.Run("a blank addition is skipped", func(t *testing.T) {
		adds := []db.AiPromptLanguageAddition{addition(1, 0, "   ", "de")}
		if got := ApplyLanguageAdditions(base, adds, "de"); got != base {
			t.Errorf("a blank addition must not add whitespace, got %q", got)
		}
	})

	t.Run("no additions at all", func(t *testing.T) {
		if got := ApplyLanguageAdditions(base, nil, "de"); got != base {
			t.Errorf("got %q, want the base text unchanged", got)
		}
	})
}

func TestNormalizeLanguages(t *testing.T) {
	got := NormalizeLanguages([]string{"DE", " fr ", "", "pt_BR", "de", "  "})
	want := []string{"de", "fr", "pt-br"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (order is the author's)", got, want)
		}
	}
}
