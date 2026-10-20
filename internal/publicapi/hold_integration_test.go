package publicapi_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// A segment another member holds refuses an API draft and confirm with 409
// segment_held in the standard error envelope, and stays as it was; the
// holder's own key writes it.
func TestIntegrationV1_HeldSegmentRefused(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "SegHeld")
	ctx := context.Background()
	id := f.ids["translationID"]
	holder := f.users["project_manager"]
	testutil.SeedHold(t, q, f.tenant.ID, f.job.ID, id, holder.ID)
	before, err := q.GetTranslation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	seg := fmt.Sprintf("%s/%d", f.path("/jobs/{jobID}/segments"), id)

	for _, w := range []struct{ method, suffix string }{{http.MethodPut, "/draft"}, {http.MethodPost, "/confirm"}} {
		rec := f.client.do(w.method, seg+w.suffix, map[string]any{"text": "Hallo Welt."}, f.key(t, "owner"))
		wantStatus(t, rec, http.StatusConflict)
		if code := errorCode(t, rec); code != "segment_held" {
			t.Fatalf("%s: code = %q, want segment_held", w.suffix, code)
		}
		if !strings.Contains(rec.Body.String(), holder.Name) {
			t.Errorf("%s: the message does not name the holder: %s", w.suffix, rec.Body.String())
		}
	}
	got, err := q.GetTranslation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveText != nil || got.Status != before.Status || !got.UpdatedAt.Time.Equal(before.UpdatedAt.Time) {
		t.Fatalf("held segment changed: %v %s", got.ActiveText, got.Status)
	}

	rec := f.client.do(http.MethodPut, seg+"/draft", map[string]any{"text": "Hallo Welt."}, f.key(t, "project_manager"))
	wantStatus(t, rec, http.StatusOK)
	if s := testutil.DecodeData[publicapi.SegmentState](t, rec); s.TargetText == nil || *s.TargetText != "Hallo Welt." {
		t.Fatalf("holder's draft = %+v", s)
	}
}
