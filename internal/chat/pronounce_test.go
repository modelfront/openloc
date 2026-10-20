package chat

import "testing"

func TestPronounce(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Click a target cell to edit; Ctrl+Enter confirms and moves on.",
			"Click a target cell to edit. Control Enter confirms and moves on."},
		{"Memory matches (TM) and MT suggestions.", "Memory matches, translation memory, and machine translation suggestions."},
		{"Import TMX or TBX files into your TMs.", "Import T M X or T B X files into your translation memories."},
		{"QA → APE", "Q A, then automatic post-editing"},
		{"Cmd + S saves.", "Control S saves."},
		// Whole words, case-sensitive: these stay as they are.
		{"The atm and Tm and MTL and CATALOG stay.", "The atm and Tm and MTL and CATALOG stay."},
		{"Terms & comments", "Terms and comments"},
	}
	for _, c := range cases {
		if got := pronounce(c.in); got != c.want {
			t.Errorf("pronounce(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// A spoken line wins over the card; the title is never read.
func TestSpeechTextPrefersSayAndSkipsTitle(t *testing.T) {
	if got := speechText("Next, let's open the editor.", "Editor: segments."); got != "Next, let's open the editor." {
		t.Errorf("with say: %q", got)
	}
	if got := speechText("", "Source on the left; translation on the right."); got != "Source on the left. translation on the right." {
		t.Errorf("without say: %q", got)
	}
}
