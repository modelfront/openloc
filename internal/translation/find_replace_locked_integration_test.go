package translation_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// frLockedJob is a job with two unlocked segments and one locked segment, all
// containing "Hello".
type frLockedJob struct {
	jobID            int64
	open1, open2, lk int64 // translation ids; lk is locked
}

func seedFRLockedJob(t *testing.T, q db.Querier, projectID int64, name string) frLockedJob {
	t.Helper()
	file := testutil.SeedFile(t, q, projectID, name+".txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	ids := make([]int64, 3)
	for i, text := range []string{"Hello one", "Hello two", "Hello locked"} {
		seg := testutil.SeedSegment(t, q, file.ID, fmt.Sprintf("Source %d", i), int32(i+1))
		tr, err := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
			SegmentID: seg.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
			ActiveText: &text, Origin: origin,
		})
		if err != nil {
			t.Fatalf("create translation %d: %v", i, err)
		}
		ids[i] = tr.ID
	}
	if err := q.LockTranslation(testutil.Ctx(), ids[2]); err != nil {
		t.Fatalf("lock translation: %v", err)
	}
	return frLockedJob{jobID: job.ID, open1: ids[0], open2: ids[1], lk: ids[2]}
}

type frLockedResp struct {
	Data struct {
		Matches        int     `json:"matches"`
		Segments       int     `json:"segments"`
		TranslationIDs []int64 `json:"translation_ids"`
		Replaced       int     `json:"replaced"`
		Locked         int     `json:"locked"`
	} `json:"data"`
}

// Find & Replace leaves locked segments out unless include_locked is set. With
// it, Find lists them for anyone; Replace writes them only for owner/admin —
// the roles that may unlock a segment — and reports them as locked otherwise.
func TestIntegrationFindReplace_IncludeLocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	ownerID, tenantID := registerUser(t, q, "fr-locked")
	projectID := createProject(t, router, "FR Locked Project", ownerID, tenantID)
	pm := testutil.SeedUserWithRole(t, q, tenantID, "fr-locked-pm@test.com", "password123", "project_manager")

	call := func(t *testing.T, userID int64, role string, jobID int64, body map[string]any) frLockedResp {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", jobID), body, userID, tenantID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var r frLockedResp
		testutil.DecodeJSON(t, rec, &r)
		return r
	}
	textOf := func(t *testing.T, id int64) (string, bool) {
		t.Helper()
		tr, err := q.GetTranslation(testutil.Ctx(), id)
		if err != nil {
			t.Fatalf("get translation %d: %v", id, err)
		}
		if tr.ActiveText == nil {
			return "", tr.Locked
		}
		return *tr.ActiveText, tr.Locked
	}

	t.Run("find skips locked segments by default", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "find-default")
		r := call(t, ownerID, "owner", j.jobID, map[string]any{"find": "Hello", "mode": "find"})
		if r.Data.Segments != 2 || r.Data.Matches != 2 || slices.Contains(r.Data.TranslationIDs, j.lk) {
			t.Errorf("find = %d segments, %d matches, ids %v; want 2, 2, without locked %d", r.Data.Segments, r.Data.Matches, r.Data.TranslationIDs, j.lk)
		}
	})

	t.Run("find with include_locked lists locked segments for any role", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "find-include")
		r := call(t, pm.ID, "project_manager", j.jobID, map[string]any{"find": "Hello", "mode": "find", "include_locked": true})
		if r.Data.Segments != 3 || !slices.Contains(r.Data.TranslationIDs, j.lk) {
			t.Errorf("find = %d segments, ids %v; want 3 including locked %d", r.Data.Segments, r.Data.TranslationIDs, j.lk)
		}
	})

	t.Run("replace leaves locked segments out by default", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "replace-default")
		r := call(t, ownerID, "owner", j.jobID, map[string]any{"find": "Hello", "replace": "Hola", "mode": "replace_all"})
		if r.Data.Replaced != 2 || r.Data.Segments != 2 || r.Data.Locked != 0 {
			t.Errorf("replace = %d replaced, %d segments, %d locked; want 2, 2, 0", r.Data.Replaced, r.Data.Segments, r.Data.Locked)
		}
		if text, locked := textOf(t, j.lk); text != "Hello locked" || !locked {
			t.Errorf("locked segment = %q (locked=%v), want it unchanged and locked", text, locked)
		}
		if text, _ := textOf(t, j.open1); text != "Hola one" {
			t.Errorf("unlocked segment = %q, want %q", text, "Hola one")
		}
	})

	// A replaced segment is a draft, and a locked segment is always approved,
	// so the replace unlocks it.
	t.Run("replace with include_locked writes and unlocks locked segments for owner/admin", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "replace-owner")
		r := call(t, ownerID, "owner", j.jobID, map[string]any{"find": "Hello", "replace": "Hola", "mode": "replace_all", "include_locked": true})
		if r.Data.Replaced != 3 || r.Data.Segments != 3 || r.Data.Locked != 0 {
			t.Errorf("replace = %d replaced, %d segments, %d locked; want 3, 3, 0", r.Data.Replaced, r.Data.Segments, r.Data.Locked)
		}
		if text, locked := textOf(t, j.lk); text != "Hola locked" || locked {
			t.Errorf("locked segment = %q (locked=%v), want %q and unlocked", text, locked, "Hola locked")
		}
	})

	t.Run("replace with include_locked never writes locked segments for other roles", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "replace-pm")
		r := call(t, pm.ID, "project_manager", j.jobID, map[string]any{"find": "Hello", "replace": "Hola", "mode": "replace_all", "include_locked": true})
		if r.Data.Replaced != 2 || r.Data.Segments != 2 || r.Data.Locked != 1 {
			t.Errorf("replace = %d replaced, %d segments, %d locked; want 2, 2, 1", r.Data.Replaced, r.Data.Segments, r.Data.Locked)
		}
		if text, _ := textOf(t, j.lk); text != "Hello locked" {
			t.Errorf("locked segment = %q, want it unchanged", text)
		}
	})

	t.Run("replace_one on a locked segment follows the same rule", func(t *testing.T) {
		j := seedFRLockedJob(t, q, projectID, "replace-one")
		r := call(t, ownerID, "owner", j.jobID, map[string]any{"find": "Hello", "replace": "Hola", "mode": "replace_one", "translation_id": j.lk})
		if r.Data.Replaced != 0 {
			t.Errorf("replace_one without include_locked replaced %d, want 0", r.Data.Replaced)
		}
		r = call(t, ownerID, "owner", j.jobID, map[string]any{"find": "Hello", "replace": "Hola", "mode": "replace_one", "translation_id": j.lk, "include_locked": true})
		if r.Data.Replaced != 1 {
			t.Errorf("replace_one with include_locked as owner replaced %d, want 1", r.Data.Replaced)
		}
		if text, _ := textOf(t, j.lk); text != "Hola locked" {
			t.Errorf("locked segment = %q, want %q", text, "Hola locked")
		}
	})
}
