package translation_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationBulkWrites_QueueBehindStepChange pins that each bulk
// translation write that meets a step change on its job waits for it instead
// of deadlocking with its recount, then answers 200 with every row written.
func TestIntegrationBulkWrites_QueueBehindStepChange(t *testing.T) {
	var propagateSource int64
	cases := []struct {
		name string
		// seed returns the rows the write changes, in id order.
		seed  func(t *testing.T, f *lockFixture) []int64
		path  func(f *lockFixture) string
		body  func(ids []int64) map[string]any
		check func(t *testing.T, tr db.Translation)
	}{
		{
			name: "confirm",
			seed: func(t *testing.T, f *lockFixture) []int64 { return drafts(t, f, "Eins", "Zwei", "Drei") },
			path: func(f *lockFixture) string { return fmt.Sprintf("/api/jobs/%d/translations/confirm", f.jobID) },
			body: idsBody,
			check: func(t *testing.T, tr db.Translation) {
				if tr.Status != db.TranslationStatusApproved {
					t.Errorf("translation %d: status %s, want approved", tr.ID, tr.Status)
				}
			},
		},
		{
			name: "confirm machine-approved",
			seed: func(t *testing.T, f *lockFixture) []int64 {
				ids := drafts(t, f, "Eins", "Zwei", "Drei")
				for _, id := range ids {
					if _, err := f.pool.Exec(t.Context(), `UPDATE translations SET predict_outcome = 'approved' WHERE id = $1`, id); err != nil {
						t.Fatal(err)
					}
				}
				return ids
			},
			path: func(f *lockFixture) string { return fmt.Sprintf("/api/jobs/%d/translations/confirm", f.jobID) },
			body: idsBody,
			check: func(t *testing.T, tr db.Translation) {
				if tr.Status != db.TranslationStatusAutoApproved {
					t.Errorf("translation %d: status %s, want auto_approved", tr.ID, tr.Status)
				}
			},
		},
		{
			name: "lock",
			seed: func(t *testing.T, f *lockFixture) []int64 { return drafts(t, f, "Eins", "Zwei", "Drei") },
			path: func(f *lockFixture) string { return fmt.Sprintf("/api/jobs/%d/translations/lock", f.jobID) },
			body: idsBody,
			check: func(t *testing.T, tr db.Translation) {
				if !tr.Locked {
					t.Errorf("translation %d not locked", tr.ID)
				}
			},
		},
		{
			name: "unlock",
			seed: func(t *testing.T, f *lockFixture) []int64 {
				ids := drafts(t, f, "Eins", "Zwei", "Drei")
				for _, id := range ids {
					if _, err := f.pool.Exec(t.Context(), `UPDATE translations SET locked = true WHERE id = $1`, id); err != nil {
						t.Fatal(err)
					}
				}
				return ids
			},
			path: func(f *lockFixture) string { return fmt.Sprintf("/api/jobs/%d/translations/unlock", f.jobID) },
			body: idsBody,
			check: func(t *testing.T, tr db.Translation) {
				if tr.Locked {
					t.Errorf("translation %d still locked", tr.ID)
				}
			},
		},
		{
			name: "propagate",
			seed: func(t *testing.T, f *lockFixture) []int64 {
				propagateSource = f.row(t, "Same", db.TranslationStatusNeedsReview, "Gleich")
				return []int64{
					f.row(t, "Same", db.TranslationStatusNeedsReview, "Eins"),
					f.row(t, "Same", db.TranslationStatusNeedsReview, "Zwei"),
					f.row(t, "Same", db.TranslationStatusNeedsReview, "Drei"),
				}
			},
			path: func(f *lockFixture) string { return fmt.Sprintf("/api/files/%d/propagate", f.fileID) },
			body: func(ids []int64) map[string]any {
				return map[string]any{"source_text": "Same", "target_language": "de", "human_text": "Neu", "exclude_id": propagateSource}
			},
			check: func(t *testing.T, tr db.Translation) {
				if got := text(tr); got != "Neu" {
					t.Errorf("translation %d: text %q, want the propagated text", tr.ID, got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupLock(t)
			ids := tc.seed(t, f)
			testutil.ExpectQueuedBehindStepChange(t, f.pool, f.jobID, ids[len(ids)-1], func() error {
				rec := f.post(t, tc.path(f), f.pmID, "project_manager", tc.body(ids))
				if rec.Code != http.StatusOK {
					return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
				}
				return nil
			})
			for _, id := range ids {
				tc.check(t, f.get(t, id))
			}
		})
	}
}

// drafts seeds one needs_review row per text, in id order.
func drafts(t *testing.T, f *lockFixture, texts ...string) []int64 {
	t.Helper()
	ids := make([]int64, len(texts))
	for i, text := range texts {
		ids[i] = f.row(t, fmt.Sprintf("Source %d", i), db.TranslationStatusNeedsReview, text)
	}
	return ids
}

func idsBody(ids []int64) map[string]any { return map[string]any{"translation_ids": ids} }

// TestIntegrationLockSelection_ApprovalsNameTheStepAfterTheStepChange pins
// that a bulk lock queued behind a step change records its approvals in the
// task the step change moved the job to.
func TestIntegrationLockSelection_ApprovalsNameTheStepAfterTheStepChange(t *testing.T) {
	f := setupLock(t)
	ctx := t.Context()
	ids := drafts(t, f, "Eins", "Zwei", "Drei")
	var profileID int64
	if err := f.pool.QueryRow(ctx, `SELECT p.settings_profile_id FROM files fi JOIN projects p ON p.id = fi.project_id WHERE fi.id = $1`, f.fileID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	review, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: profileID, Name: "review", StepOrder: 1, RoleRequired: "linguist",
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: f.jobID, WorkflowStepID: review.ID, AssignedTo: &f.lingID, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	testutil.ExpectQueuedBehindStepChange(t, f.pool, f.jobID, ids[len(ids)-1], func() error {
		rec := f.post(t, fmt.Sprintf("/api/jobs/%d/translations/lock", f.jobID), f.pmID, "project_manager", idsBody(ids))
		if rec.Code != http.StatusOK {
			return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
		}
		return nil
	}, next.ID)

	for _, id := range ids {
		events := f.approvedEvents(t, id)
		if len(events) != 1 || events[0].JobStepAssignmentID == nil || *events[0].JobStepAssignmentID != next.ID {
			t.Errorf("translation %d: approvals %+v, want one in task %d", id, events, next.ID)
		}
	}
}
