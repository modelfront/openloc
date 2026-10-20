package publicapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/ptr"
)

// A v1 draft or confirm that keeps the segment's current text keeps its
// inline tags; a confirm with no text confirms the translation as it stands,
// tags included. New text replaces the tags, the same as in the app.
func TestIntegrationV1_SegmentWritesKeepTagsOfUnchangedText(t *testing.T) {
	pool, q, router, cleanup := newRouterWithPool(t)
	defer cleanup()
	f := newFixture(t, q, router, "SegRich")
	ctx := context.Background()
	id := f.ids["translationID"]
	const rich = `Hallo <g id="1">Welt</g>.`
	if _, err := pool.Exec(ctx, `UPDATE segments SET source_rich_text = 'Hello <g id="1">world</g>.'
		WHERE id = (SELECT segment_id FROM translations WHERE id = $1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = 'Hallo Welt.', active_rich_text = $2 WHERE id = $1`, id, rich); err != nil {
		t.Fatal(err)
	}
	seg := fmt.Sprintf("%s/%d", f.path("/jobs/{jobID}/segments"), id)
	storedRich := func() string {
		t.Helper()
		got, err := q.GetTranslation(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ptr.Deref(got.ActiveRichText)
	}

	rec := f.client.do(http.MethodPut, seg+"/draft", map[string]any{"text": "Hallo Welt."}, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	if got := storedRich(); got != rich {
		t.Errorf("draft of the same text: rich = %q, want %q", got, rich)
	}

	rec = f.client.do(http.MethodPost, seg+"/confirm", map[string]any{}, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	got, err := q.GetTranslation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "approved" || ptr.Deref(got.ActiveRichText) != rich {
		t.Fatalf("confirm without text: status %s, rich %q, want approved with %q", got.Status, ptr.Deref(got.ActiveRichText), rich)
	}

	rec = f.client.do(http.MethodPut, seg+"/draft", map[string]any{"text": "Hallo, Welt."}, f.key(t, "owner"))
	wantStatus(t, rec, http.StatusOK)
	if got := storedRich(); got != "" {
		t.Fatalf("draft of new text: rich = %q, want none", got)
	}
}
