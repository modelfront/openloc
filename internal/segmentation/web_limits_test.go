package segmentation

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The preview text box stops at the same length the server accepts.
func TestWebPreviewLimitMatches(t *testing.T) {
	ts, err := os.ReadFile("../../web/src/lib/segmentation.ts")
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("export const MAX_PREVIEW_TEXT = %d\n", MaxPreviewText); !strings.Contains(string(ts), want) {
		t.Errorf("web/src/lib/segmentation.ts does not hold %q", want)
	}
}

// The New project form takes the any-language built-in by the same key
// prefix the server suggests it by.
func TestWebDefaultBuiltinPrefixMatches(t *testing.T) {
	ts, err := os.ReadFile("../../web/src/hooks/useSegmentationProfiles.ts")
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("const DEFAULT_BUILTIN_PREFIX = '%s'\n", DefaultKeyPrefix); !strings.Contains(string(ts), want) {
		t.Errorf("web/src/hooks/useSegmentationProfiles.ts does not hold %q", want)
	}
}
