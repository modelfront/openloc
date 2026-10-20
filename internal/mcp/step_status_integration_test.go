package mcp_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// A listed segment carries how the last finished workflow step left it.
func TestIntegrationMCPSegmentsPreviousStep(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	job := seedJobWithSegments(t, f.q, f.projectA.ID, "prev.txt", "de", []string{"Hello", "World"})
	stepID := testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, job.ID, nil)
	if err := stepround.Open(ctx, f.q, job.ID, stepID, stepround.Keep); err != nil {
		t.Fatal(err)
	}
	trs, err := f.q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: job.FileID, TargetLanguage: "de"})
	if err != nil || len(trs) != 2 {
		t.Fatalf("translations = %d (err %v)", len(trs), err)
	}
	text := "Hallo"
	if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: trs[0].ID, HumanText: &text, EditedBy: &f.ownerA.ID}); err != nil {
		t.Fatal(err)
	}
	if err := stepround.Close(ctx, f.q, stepID, stepround.Completed); err != nil {
		t.Fatal(err)
	}

	var out struct {
		Segments []struct {
			TranslationID int64 `json:"translation_id"`
			PreviousStep  *struct {
				StepName  string `json:"step_name"`
				Status    string `json:"status"`
				Edited    bool   `json:"edited"`
				EndReason string `json:"end_reason"`
			} `json:"previous_step"`
		} `json:"segments"`
	}
	s := connect(t, f.url, f.tokenA)
	if isErr, text := callTool(t, s, "openloc_list_segments", map[string]any{"job_id": job.ID}, &out); isErr {
		t.Fatalf("tool error: %s", text)
	}
	if len(out.Segments) != 2 {
		t.Fatalf("segments = %+v", out.Segments)
	}
	got := map[int64]string{}
	for _, seg := range out.Segments {
		p := seg.PreviousStep
		if p == nil {
			t.Fatalf("segment %d has no previous_step", seg.TranslationID)
		}
		if p.StepName != "translate" || p.EndReason != "completed" {
			t.Errorf("previous_step = %+v", p)
		}
		got[seg.TranslationID] = p.Status
		if seg.TranslationID == trs[0].ID && !p.Edited {
			t.Error("the confirmed segment was edited in the step")
		}
	}
	if got[trs[0].ID] != "approved" || got[trs[1].ID] != "needs_review" {
		t.Errorf("previous statuses = %v, want approved / needs_review", got)
	}
}
