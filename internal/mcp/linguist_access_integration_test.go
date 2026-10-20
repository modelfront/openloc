package mcp_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A linguist reaches only jobs they are assigned to — the same gate REST
// applies with auth.RequireLinguistJobAccess / RequireLinguistTranslationAccess.
// Every job- and segment-taking tool on the linguist tier is covered, because
// the direct backend bypasses that middleware.
func TestIntegrationMCPLinguistAssignmentGate(t *testing.T) {
	f := newFixture(t)
	ownerTok := writeToken(t, f, f.tenantA.ID, f.ownerA.ID)
	lingTok := writeToken(t, f, f.tenantA.ID, f.linguistA.ID)

	translationID, _ := firstTranslation(t, connect(t, f.url, ownerTok), f.jobA.ID)
	ling := connect(t, f.url, lingTok)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"openloc_get_job", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_get_job_leverage", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_list_segments", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_get_qa_report", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_run_qa", map[string]any{"job_id": f.jobA.ID}},
		{"openloc_add_comment", map[string]any{"translation_id": translationID, "content": "should not land"}},
		{"openloc_lock_segment", map[string]any{"translation_id": translationID}},
		{"openloc_save_draft", map[string]any{"translation_id": translationID, "text": "should not land"}},
		{"openloc_ignore_qa_finding", map[string]any{"translation_id": translationID, "check_type": "punctuation"}},
	}

	t.Run("unassigned linguist is refused", func(t *testing.T) {
		for _, c := range calls {
			isErr, text := callTool(t, ling, c.tool, c.args, nil)
			if !isErr {
				t.Errorf("%s: unassigned linguist was allowed (%s)", c.tool, text)
				continue
			}
			assertOpaqueNotFound(t, text)
		}
		got, err := f.q.GetTranslationTenant(context.Background(), db.GetTranslationTenantParams{
			ID: translationID, TenantID: f.tenantA.ID,
		})
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.Locked {
			t.Error("segment was locked by an unassigned linguist")
		}
		if got.ActiveText != nil && *got.ActiveText == "should not land" {
			t.Error("draft was saved by an unassigned linguist")
		}
	})

	t.Run("assigned linguist is allowed", func(t *testing.T) {
		testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
		for _, tool := range []string{"openloc_get_job", "openloc_list_segments"} {
			if isErr, text := callTool(t, ling, tool, map[string]any{"job_id": f.jobA.ID}, nil); isErr {
				t.Errorf("%s: assigned linguist refused: %s", tool, text)
			}
		}
		if isErr, text := callTool(t, ling, "openloc_save_draft",
			map[string]any{"translation_id": translationID, "text": "Hallo Welt"}, nil); isErr {
			t.Errorf("save_draft: assigned linguist refused: %s", text)
		}
	})

	// The DE job's linguist holds nothing in the same file's FR job.
	t.Run("other language of the assigned file is refused", func(t *testing.T) {
		seg := testutil.SeedSegment(t, f.q, f.jobA.FileID, "Fourth segment", 99)
		fr := testutil.SeedTranslation(t, f.q, seg.ID, "fr", db.TranslationStatusNeedsReview)
		testutil.SeedJob(t, f.q, f.jobA.FileID, "fr")
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"openloc_save_draft", map[string]any{"translation_id": fr.ID, "text": "should not land"}},
			{"openloc_add_comment", map[string]any{"translation_id": fr.ID, "content": "should not land"}},
		} {
			isErr, text := callTool(t, ling, c.tool, c.args, nil)
			if !isErr {
				t.Errorf("%s: DE linguist reached an FR row (%s)", c.tool, text)
				continue
			}
			assertOpaqueNotFound(t, text)
		}
		got, err := f.q.GetTranslation(context.Background(), fr.ID)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.ActiveText != nil && *got.ActiveText == "should not land" {
			t.Error("draft was saved into another language by the DE linguist")
		}
	})
}
