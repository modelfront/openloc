package translation_test

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Writing a locked row is opt-in. By default batch draft reports a locked row
// and leaves it alone: a lock means "don't touch", and unlock is owner/admin
// only. Spell check's "All segments (includes locked)" scope asks for the
// write with `include_locked`; nothing else does. This runs as the owner; the
// role gate on the flag is TestIntegrationBatchDraft_IncludeLockedIsAdminOnly.
func TestIntegrationBatchDraft_IncludeLockedWritesLockedRows(t *testing.T) {
	s := setupBatchScenario(t, 1)
	if err := s.q.LockTranslation(testutil.Ctx(), s.trs[0].ID); err != nil {
		t.Fatalf("lock: %v", err)
	}
	fixes := []map[string]any{{"translation_id": s.trs[0].ID, "human_text": "locked fix"}}

	// Default: refused, reported, unchanged.
	def := s.post(t, fixes)
	if len(def.Data.Applied) != 0 || len(def.Data.Locked) != 1 || def.Data.Locked[0] != s.trs[0].ID {
		t.Fatalf("without include_locked: want the row reported as locked and not written; got applied=%d locked=%v",
			len(def.Data.Applied), def.Data.Locked)
	}
	got, err := s.q.GetTranslation(testutil.Ctx(), s.trs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveText == nil || *got.ActiveText != *s.trs[0].ActiveText || got.Status != s.trs[0].Status {
		t.Errorf("locked row = %v / %s, want %q / %s unchanged",
			got.ActiveText, got.Status, *s.trs[0].ActiveText, s.trs[0].Status)
	}

	// Opted in: written, as a draft rather than a confirmation.
	on := s.postWith(t, map[string]any{"fixes": fixes, "include_locked": true})
	if len(on.Data.Applied) != 1 {
		t.Fatalf("with include_locked: want 1 applied, got %d (locked=%v rejected=%v)",
			len(on.Data.Applied), on.Data.Locked, on.Data.Rejected)
	}
	if len(on.Data.Locked) != 0 {
		t.Errorf("with include_locked: want nothing reported as locked, got %v", on.Data.Locked)
	}
	applied := on.Data.Applied[0]
	if applied.ActiveText == nil || *applied.ActiveText != "locked fix" {
		t.Errorf("active_text = %v, want %q", applied.ActiveText, "locked fix")
	}
	if applied.Status != db.TranslationStatusNeedsReview {
		t.Errorf("status = %s, want needs_review — the lock bypass must not also confirm", applied.Status)
	}
	// A draft is never locked (a locked segment is always approved), so the
	// write through the lock unlocks the segment.
	if applied.Locked {
		t.Error("the segment should be unlocked after the write")
	}
}

// `include_locked` is honoured for owners and admins only — the roles that may
// unlock a segment. For any other role the server treats the flag as false:
// the locked row comes back in `locked` and keeps its text, with no 403, so a
// linguist cannot write through a lock from spell check's "All segments" scope.
func TestIntegrationBatchDraft_IncludeLockedIsAdminOnly(t *testing.T) {
	roles := []struct {
		role    string
		written bool
	}{
		{"linguist", false},
		{"project_manager", false},
		{"admin", true},
		{"owner", true},
	}
	s := setupBatchScenario(t, len(roles))
	ctx := testutil.Ctx()
	for _, tr := range s.trs {
		if err := s.q.LockTranslation(ctx, tr.ID); err != nil {
			t.Fatalf("lock: %v", err)
		}
	}
	// The file route admits a linguist only through a job step assigned to
	// them; the other roles pass that gate on role alone.
	job := testutil.SeedJob(t, s.q, s.fileID, "de")

	for i, tc := range roles {
		t.Run(tc.role, func(t *testing.T) {
			member := testutil.SeedUserWithRole(t, s.q, s.tenantID, tc.role+"@batchdraft.test", "pw", tc.role)
			if tc.role == "linguist" {
				testutil.SeedJobStep(t, s.q, s.profileID, job.ID, &member.ID)
			}
			row := s.trs[i]
			res := s.postAs(t, map[string]any{
				"fixes":          []map[string]any{{"translation_id": row.ID, "human_text": "locked fix"}},
				"include_locked": true,
			}, member.ID, tc.role)

			got, err := s.q.GetTranslation(ctx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.written {
				if len(res.Data.Applied) != 1 || len(res.Data.Locked) != 0 {
					t.Fatalf("%s with include_locked: want 1 applied and none locked, got applied=%d locked=%v",
						tc.role, len(res.Data.Applied), res.Data.Locked)
				}
				if got.ActiveText == nil || *got.ActiveText != "locked fix" {
					t.Errorf("%s: active_text = %v, want %q", tc.role, got.ActiveText, "locked fix")
				}
				return
			}
			if len(res.Data.Applied) != 0 || len(res.Data.Locked) != 1 || res.Data.Locked[0] != row.ID {
				t.Fatalf("%s with include_locked: want the row reported as locked and not written; got applied=%d locked=%v",
					tc.role, len(res.Data.Applied), res.Data.Locked)
			}
			if got.ActiveText == nil || *got.ActiveText != *row.ActiveText {
				t.Errorf("%s: locked row text = %v, want %q unchanged", tc.role, got.ActiveText, *row.ActiveText)
			}
		})
	}
}
