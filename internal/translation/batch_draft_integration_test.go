package translation_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type batchDraftResponse struct {
	Data struct {
		Applied   []db.Translation `json:"applied"`
		Conflicts []int64          `json:"conflicts"`
		Locked    []int64          `json:"locked"`
		Rejected  []int64          `json:"rejected"`
	} `json:"data"`
}

type batchScenario struct {
	router    http.Handler
	q         db.Querier
	userID    int64
	tenantID  int64
	profileID int64
	fileID    int64
	trs       []db.Translation
}

// setupBatchScenario builds a file with `n` German translations, each holding
// text and already confirmed as `approved` — the state that makes the status
// regression visible.
func setupBatchScenario(t *testing.T, n int) batchScenario {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "batchdraft")
	project := testutil.SeedProject(t, q, tenantID, "Batch Draft", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.docx", db.FileFormatDocx)

	trs := make([]db.Translation, 0, n)
	for i := 0; i < n; i++ {
		seg := testutil.SeedSegment(t, q, file.ID, fmt.Sprintf("source %d", i), int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
		text := fmt.Sprintf("die adresse %d", i)
		// UpdateTranslationHuman is the "user confirmed this" path — it leaves
		// the row at status=approved, which is the precondition under test.
		updated, err := q.UpdateTranslationHuman(testutil.Ctx(), db.UpdateTranslationHumanParams{
			ID: tr.ID, HumanText: &text, EditedBy: &userID,
		})
		if err != nil {
			t.Fatalf("seed confirmed translation: %v", err)
		}
		trs = append(trs, updated)
	}

	return batchScenario{
		router: router, q: q, userID: userID, tenantID: tenantID,
		profileID: project.SettingsProfileID, fileID: file.ID, trs: trs,
	}
}

func (s batchScenario) post(t *testing.T, fixes []map[string]any) batchDraftResponse {
	return s.postWith(t, map[string]any{"fixes": fixes})
}

// postWith sends the batch as the scenario's owner.
func (s batchScenario) postWith(t *testing.T, body map[string]any) batchDraftResponse {
	t.Helper()
	return s.postAs(t, body, s.userID, "owner")
}

// postAs sends the batch as another member of the tenant, with the given role.
func (s batchScenario) postAs(t *testing.T, body map[string]any, userID int64, role string) batchDraftResponse {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, s.router, http.MethodPut,
		fmt.Sprintf("/api/files/%d/translations/batch-draft", s.fileID),
		body, userID, s.tenantID, role)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch-draft: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp batchDraftResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return resp
}

// THE regression guard for this feature.
//
// A spell fix is an edit, and an edit downgrades an approved segment to
// needs_review. If batch-draft ever gets
// re-pointed at UpdateTranslationHuman (which sets status='approved'
// unconditionally), a "Change all" over 40 segments would silently confirm all
// 40 — signing off work nobody reviewed. This test fails loudly if that happens.
func TestIntegrationBatchDraft_DowngradesApprovedToNeedsReview(t *testing.T) {
	s := setupBatchScenario(t, 2)
	for _, tr := range s.trs {
		if tr.Status != db.TranslationStatusApproved {
			t.Fatalf("precondition: seeded status = %s, want approved", tr.Status)
		}
	}

	resp := s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "die Adresse 0"},
		{"translation_id": s.trs[1].ID, "human_text": "die Adresse 1"},
	})

	if len(resp.Data.Applied) != 2 {
		t.Fatalf("applied = %d, want 2 (conflicts=%v rejected=%v)",
			len(resp.Data.Applied), resp.Data.Conflicts, resp.Data.Rejected)
	}
	for _, tr := range resp.Data.Applied {
		if tr.Status != db.TranslationStatusNeedsReview {
			t.Errorf("translation %d status = %s, want needs_review — a bulk spell fix must not confirm segments",
				tr.ID, tr.Status)
		}
	}
}

// Rich text has to survive a bulk fix. The job find-replace handler passes only
// HumanText, leaving active_rich_text NULL and destroying tag markup; since the
// load path prefers active_rich_text, that's real data loss.
func TestIntegrationBatchDraft_PreservesRichText(t *testing.T) {
	s := setupBatchScenario(t, 1)
	rich := `<ph id="1"/>die Adresse 0`

	resp := s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "die Adresse 0", "human_rich_text": rich},
	})
	if len(resp.Data.Applied) != 1 {
		t.Fatalf("applied = %d, want 1", len(resp.Data.Applied))
	}
	got := resp.Data.Applied[0].ActiveRichText
	if got == nil || *got != rich {
		t.Errorf("active_rich_text = %v, want %q — tag markup must round-trip", got, rich)
	}
}

// The client supplies translation ids, so the endpoint must confirm each one
// belongs to the file the route authorised. A tenant-only check would let a
// linguist assigned to one file write into a sibling file.
func TestIntegrationBatchDraft_RejectsTranslationFromAnotherFile(t *testing.T) {
	s := setupBatchScenario(t, 1)

	otherProject := testutil.SeedProject(t, s.q, s.tenantID, "Other", "en", []string{"de"})
	otherFile := testutil.SeedFile(t, s.q, otherProject.ID, "other.docx", db.FileFormatDocx)
	otherSeg := testutil.SeedSegment(t, s.q, otherFile.ID, "other source", 1)
	otherTr := testutil.SeedTranslation(t, s.q, otherSeg.ID, "de", db.TranslationStatusNeedsReview)

	resp := s.post(t, []map[string]any{
		{"translation_id": otherTr.ID, "human_text": "should not land"},
	})

	if len(resp.Data.Applied) != 0 {
		t.Errorf("applied = %d, want 0 for a translation outside the route's file", len(resp.Data.Applied))
	}
	if len(resp.Data.Rejected) != 1 || resp.Data.Rejected[0] != otherTr.ID {
		t.Errorf("rejected = %v, want [%d]", resp.Data.Rejected, otherTr.ID)
	}

	fresh, err := s.q.GetTranslation(testutil.Ctx(), otherTr.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.ActiveText != nil && *fresh.ActiveText == "should not land" {
		t.Error("cross-file write landed — tenant-only scoping is not sufficient here")
	}
}

func TestIntegrationBatchDraft_ReportsLockedRows(t *testing.T) {
	s := setupBatchScenario(t, 2)
	if err := s.q.LockTranslation(testutil.Ctx(), s.trs[0].ID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	resp := s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "locked fix"},
		{"translation_id": s.trs[1].ID, "human_text": "unlocked fix"},
	})

	if len(resp.Data.Locked) != 1 || resp.Data.Locked[0] != s.trs[0].ID {
		t.Errorf("locked = %v, want [%d]", resp.Data.Locked, s.trs[0].ID)
	}
	// The unlocked sibling still applies — one locked row must not sink the batch.
	if len(resp.Data.Applied) != 1 || resp.Data.Applied[0].ID != s.trs[1].ID {
		t.Errorf("applied = %+v, want just %d", resp.Data.Applied, s.trs[1].ID)
	}
}

// Partial success is deliberate: if a colleague edited one segment mid-review,
// rolling back the whole batch would make the linguist redo every other fix.
func TestIntegrationBatchDraft_StaleVersionConflictsWithoutSinkingBatch(t *testing.T) {
	s := setupBatchScenario(t, 2)
	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)

	resp := s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "stale fix", "expected_updated_at": stale},
		{"translation_id": s.trs[1].ID, "human_text": "fresh fix"},
	})

	if len(resp.Data.Conflicts) != 1 || resp.Data.Conflicts[0] != s.trs[0].ID {
		t.Errorf("conflicts = %v, want [%d]", resp.Data.Conflicts, s.trs[0].ID)
	}
	if len(resp.Data.Applied) != 1 || resp.Data.Applied[0].ID != s.trs[1].ID {
		t.Errorf("applied = %+v, want just %d", resp.Data.Applied, s.trs[1].ID)
	}
}

func TestIntegrationBatchDraft_TenantIsolation(t *testing.T) {
	s := setupBatchScenario(t, 1)
	otherUser, otherTenant := testutil.RegisterUser(t, s.q, "batchdraft-outsider")

	rec := testutil.DoRequest(t, s.router, http.MethodPut,
		fmt.Sprintf("/api/files/%d/translations/batch-draft", s.fileID),
		map[string]any{"fixes": []map[string]any{
			{"translation_id": s.trs[0].ID, "human_text": "cross-tenant"},
		}}, otherUser, otherTenant)

	if rec.Code == http.StatusOK {
		t.Errorf("cross-tenant batch-draft returned 200; body: %s", rec.Body.String())
	}
}

// A bulk fix over approved segments takes the approval away, and that is an
// event the History panel has to show. The status assertion above proves the
// column moved; only this proves the row was written.
//
// It is also the once-per-cycle rule: the row records a TRANSITION, so a second
// bulk fix over the same (now needs_review) segments must add nothing. Without
// that half, emitting on every save would pass too — and "Change all" over 40
// segments would bury the panel in bookkeeping rows.
func TestIntegrationBatchDraft_RecordsUnapprovalOncePerCycle(t *testing.T) {
	s := setupBatchScenario(t, 2)
	ctx := testutil.Ctx()

	unapprovals := func(translationID int64) []db.ListTranslationHistoryRow {
		t.Helper()
		rows, err := s.q.ListTranslationHistory(ctx, translationID)
		if err != nil {
			t.Fatalf("list history: %v", err)
		}
		out := []db.ListTranslationHistoryRow{}
		for _, r := range rows {
			if r.Kind == db.TranslationHistoryKindUnconfirmed {
				out = append(out, r)
			}
		}
		return out
	}

	first := s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "die Adresse 0"},
		{"translation_id": s.trs[1].ID, "human_text": "die Adresse 1"},
	})
	if len(first.Data.Applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(first.Data.Applied))
	}

	for _, tr := range s.trs {
		rows := unapprovals(tr.ID)
		if len(rows) != 1 {
			t.Fatalf("translation %d: %d unconfirmed rows after the first fix, want 1", tr.ID, len(rows))
		}
		// Status events carry no text — that is what keeps them out of the
		// edit-distance baseline (`WHERE text IS NOT NULL`).
		if rows[0].Text != nil {
			t.Errorf("translation %d: unconfirmed row carries text %q, want none", tr.ID, *rows[0].Text)
		}
		if rows[0].CreatedBy == nil || *rows[0].CreatedBy != s.userID {
			t.Errorf("translation %d: unconfirmed created_by = %v, want %d", tr.ID, rows[0].CreatedBy, s.userID)
		}
	}

	// Second pass: the segments are already needs_review, so no approval is lost.
	s.post(t, []map[string]any{
		{"translation_id": s.trs[0].ID, "human_text": "die Adresse 0b"},
		{"translation_id": s.trs[1].ID, "human_text": "die Adresse 1b"},
	})
	for _, tr := range s.trs {
		if rows := unapprovals(tr.ID); len(rows) != 1 {
			t.Errorf("translation %d: %d unconfirmed rows after the second fix, want still 1 — the row records a transition, not a save",
				tr.ID, len(rows))
		}
	}
}
