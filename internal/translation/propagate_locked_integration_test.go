package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Confirming copies the translation to every identical-source segment except
// locked ones: a lock means "don't touch", so a locked repetition keeps its
// text and status, and the response does not count it.
func TestIntegrationPropagate_SkipsLockedSiblings(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "propagate-locked")
	projectID := createProject(t, router, "Propagate Locked Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "propagate-locked.txt", db.FileFormatTxt)

	seed := func(pos int32) db.Translation {
		seg := testutil.SeedSegment(t, q, file.ID, "Hello world", pos)
		return testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	}
	confirmed, locked, open := seed(1), seed(2), seed(3)

	if _, err := pool.Exec(ctx, "UPDATE translations SET active_text = 'Texto bloqueado' WHERE id = $1", locked.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.LockTranslation(ctx, locked.ID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Hello world",
		"target_language": "es",
		"human_text":      "Hola mundo",
		"exclude_id":      confirmed.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Propagated int              `json:"propagated"`
			Siblings   []db.Translation `json:"siblings"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Propagated != 1 || len(resp.Data.Siblings) != 1 || resp.Data.Siblings[0].ID != open.ID {
		t.Errorf("propagated = %d, siblings = %d, want only the unlocked row %d",
			resp.Data.Propagated, len(resp.Data.Siblings), open.ID)
	}

	gotLocked, err := q.GetTranslation(ctx, locked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotLocked.ActiveText == nil || *gotLocked.ActiveText != "Texto bloqueado" {
		t.Errorf("locked sibling text = %q, want it unchanged (Texto bloqueado)", textOf(gotLocked.ActiveText))
	}
	if gotLocked.Status != db.TranslationStatusNeedsReview {
		t.Errorf("locked sibling status = %s, want needs_review (unchanged)", gotLocked.Status)
	}

	gotOpen, err := q.GetTranslation(ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotOpen.ActiveText == nil || *gotOpen.ActiveText != "Hola mundo" || gotOpen.Status != db.TranslationStatusApproved {
		t.Errorf("unlocked sibling = %q / %s, want Hola mundo / approved", textOf(gotOpen.ActiveText), gotOpen.Status)
	}
}

func textOf(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
