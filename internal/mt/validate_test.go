package mt

import (
	"context"
	"strings"
	"testing"
)

// A catalog the engine itself publishes — ValidateAssignment refuses nothing on
// a compiled-in stand-in, so these tests cannot use one.
func published(endpoint string) publishedEngine {
	return publishedEngine{endpoint: endpoint, catalog: libreTranslateFallback()}
}

// publishedRich carries regional variants, for the direct-match path — the
// base-code tables have none, so every regional target falls back on them.
func publishedRich(endpoint string) publishedEngine {
	return publishedEngine{endpoint: endpoint, catalog: mockLanguages()}
}

func TestValidateAssignment_DirectSupport(t *testing.T) {
	// A published catalog: with a static one ValidateAssignment returns before
	// any lookup, and this would pass against an empty table.
	w := ValidateAssignment(context.Background(), "en", "es-mx", publishedRich("validate-direct"))
	if w.Rejected {
		t.Errorf("expected es-mx to be accepted (the catalog lists it): %s", w.Reason)
	}
}

// A pair that only works via base-code fallback is still a pair the engine
// takes — ResolveLocalePair does the swap, and logResolved records it.
func TestValidateAssignment_BaseCodeFallbackIsNotARejection(t *testing.T) {
	w := ValidateAssignment(context.Background(), "en", "es-mx", published("validate-fallback"))
	if w.Rejected {
		t.Errorf("expected es-mx to be accepted (falls back to es): %s", w.Reason)
	}
}

func TestValidateAssignment_Unsupported(t *testing.T) {
	w := ValidateAssignment(context.Background(), "en", "xx-yy", published("validate-unsupported"))
	if !w.Rejected {
		t.Error("expected xx-yy to be rejected (no base fallback)")
	}
	if !strings.Contains(w.Reason, "xx-yy") {
		t.Errorf("reason should name the target: %q", w.Reason)
	}
}

func TestValidateAssignment_SourceUnsupported(t *testing.T) {
	// LibreTranslate doesn't have "xx" as a source
	w := ValidateAssignment(context.Background(), "xx", "es", published("validate-source"))
	if !w.Rejected {
		t.Error("expected unsupported source 'xx' to be rejected")
	}
	if !strings.Contains(w.Reason, "source") {
		t.Errorf("reason should say it was the source side: %q", w.Reason)
	}
}
