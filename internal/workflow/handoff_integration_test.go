package workflow_test

import (
	"context"
	"encoding/json"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// Completing a step hands the job on. Any segment a person edited without
// confirming has its text only on the segment — a draft writes no text row — so
// the audit's newest version is the one from before that person started, and
// the next reviewer's edit distance would absorb their work.
//
// These tests drive Service.AdvanceJob directly; the HTTP layer around it only
// adds authz.

type handoffFixture struct {
	q        db.Querier
	svc      *workflow.Service
	ctx      context.Context
	tenantID int64
	editorID int64
	jobID    int64
	stepID   int64
	fileID   int64
	segID    int64
	// Whoever clicks Complete. Deliberately NOT the editor: the flush's whole
	// promise is that the row is credited to the person who typed the text, and
	// one user playing both roles cannot tell the two apart.
	completerID int64
}

func setupHandoff(t *testing.T) handoffFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	editorID, tenantID := testutil.RegisterUser(t, q, "handoff")
	completer := testutil.SeedUserWithRole(t, q, tenantID, "handoff-pm@test.com", "pass", "project_manager")
	proj := testutil.SeedProject(t, q, tenantID, "Handoff", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "handoff.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Meaning of grizzly", 1)
	job := testutil.SeedJob(t, q, file.ID, "es")
	stepID := testutil.SeedJobStep(t, q, proj.SettingsProfileID, job.ID, &editorID)

	return handoffFixture{
		q: q, svc: workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil), ctx: ctx,
		tenantID: tenantID, editorID: editorID, completerID: completer.ID,
		jobID: job.ID, stepID: stepID, fileID: file.ID, segID: seg.ID,
	}
}

// seedMTTranslation gives the segment machine text plus the matching history
// row, so there is a real baseline for the flush to measure against.
func (f handoffFixture) seedMTTranslation(t *testing.T, mtText string) db.Translation {
	t.Helper()
	tr := testutil.SeedTranslation(t, f.q, f.segID, "es", db.TranslationStatusNeedsReview)
	if _, err := f.q.UpdateTranslationMT(f.ctx, db.UpdateTranslationMTParams{
		ID: tr.ID, ActiveText: &mtText,
	}); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	if err := pipeline.EmitAutomationHistoryInTx(f.ctx, f.q, pipeline.AutomationEmitOpts{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt,
		Origin: db.TranslationOriginMt, Text: &mtText,
	}); err != nil {
		t.Fatalf("seed MT history: %v", err)
	}
	return tr
}

func (f handoffFixture) advance(t *testing.T, auto bool) {
	t.Helper()
	active, err := f.q.GetActiveStepForJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("get active step: %v", err)
	}
	// The manager completes the step, not the translator who typed the text.
	actor := f.completerID
	if _, err := f.svc.AdvanceJob(f.ctx, f.tenantID, f.jobID, active, &actor, nil, auto, nil); err != nil {
		t.Fatalf("advance job: %v", err)
	}
}

func (f handoffFixture) history(t *testing.T, translationID int64) []db.ListTranslationHistoryRow {
	t.Helper()
	rows, err := f.q.ListTranslationHistory(f.ctx, translationID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	return rows
}

func TestIntegrationHandoff_RecordsUnconfirmedEdit(t *testing.T) {
	f := setupHandoff(t)
	const mtText = "Significado de grizzly"
	const delivered = "Significado de grizzly, el oso"
	tr := f.seedMTTranslation(t, mtText)

	// The translator types and never confirms: only the draft autosave ran.
	if _, err := f.q.SaveTranslationDraft(f.ctx, db.SaveTranslationDraftParams{
		ID: tr.ID, HumanText: ptr.To(delivered), EditedBy: &f.editorID,
	}); err != nil {
		t.Fatalf("draft save: %v", err)
	}

	f.advance(t, false)

	rows := f.history(t, tr.ID)
	if len(rows) == 0 {
		t.Fatal("no history at all")
	}
	newest := rows[0] // ListTranslationHistory is newest-first
	if newest.Kind != db.TranslationHistoryKindHuman {
		t.Fatalf("newest kind = %s, want human", newest.Kind)
	}
	if newest.Text == nil || *newest.Text != delivered {
		t.Errorf("newest.text = %v, want the delivered text %q", newest.Text, delivered)
	}
	// The point of the whole flush: credited to whoever typed, never to whoever
	// clicked Complete. A PM completing on someone's behalf must not take their
	// words in `words_processed` or the per-step report.
	if newest.CreatedBy == nil || *newest.CreatedBy != f.editorID {
		t.Errorf("newest.created_by = %v, want the editor %d", newest.CreatedBy, f.editorID)
	}
	if newest.CreatedBy != nil && *newest.CreatedBy == f.completerID {
		t.Errorf("newest.created_by = the completer %d — the flush credited the wrong person", f.completerID)
	}
	// ", el oso" — 8 insertions on top of the MT text.
	if newest.EditDistance == nil {
		t.Errorf("newest.edit_distance is NULL, want 8 (the distance from the MT text)")
	} else if *newest.EditDistance != 8 {
		t.Errorf("newest.edit_distance = %d, want 8 (the distance from the MT text)", *newest.EditDistance)
	}
	var details map[string]any
	if err := json.Unmarshal(newest.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["source"] != "step_handoff" {
		t.Errorf("details.source = %v, want step_handoff", details["source"])
	}

	// The whole point: the next step's confirm now measures against what was
	// actually handed over, not the version from before this step.
	baseline, err := f.q.GetLatestHistoryTextForTranslation(f.ctx, tr.ID)
	if err != nil {
		t.Fatalf("baseline lookup: %v", err)
	}
	if baseline == nil || *baseline != delivered {
		t.Errorf("edit-distance baseline = %v, want the delivered text — otherwise the next "+
			"reviewer is credited with this translator's work", baseline)
	}
}

func TestIntegrationHandoff_ConfirmedSegmentsAreLeftAlone(t *testing.T) {
	f := setupHandoff(t)
	const mtText = "Significado de grizzly"
	tr := f.seedMTTranslation(t, mtText)

	// Confirmed the normal way: the confirm already wrote its own row.
	if _, err := f.q.UpdateTranslationHuman(f.ctx, db.UpdateTranslationHumanParams{
		ID: tr.ID, HumanText: ptr.To("Significado de grizzly, el oso"), EditedBy: &f.editorID,
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	pipeline.EmitHumanHistory(f.ctx, f.q, pipeline.HumanEmitOpts{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman,
		Text: ptr.To("Significado de grizzly, el oso"), CreatedBy: &f.editorID,
	})

	before := len(f.history(t, tr.ID))
	f.advance(t, false)
	if after := len(f.history(t, tr.ID)); after != before {
		t.Errorf("history grew from %d to %d rows — a confirmed segment has nothing to flush", before, after)
	}
}

// An `ai` step completing itself has no human drafts behind it.
func TestIntegrationHandoff_AutoCompletionFlushesNothing(t *testing.T) {
	f := setupHandoff(t)
	const mtText = "Significado de grizzly"
	tr := f.seedMTTranslation(t, mtText)

	if _, err := f.q.SaveTranslationDraft(f.ctx, db.SaveTranslationDraftParams{
		ID: tr.ID, HumanText: ptr.To("Significado de grizzly, el oso"), EditedBy: &f.editorID,
	}); err != nil {
		t.Fatalf("draft save: %v", err)
	}

	before := len(f.history(t, tr.ID))
	f.advance(t, true)
	if after := len(f.history(t, tr.ID)); after != before {
		t.Errorf("history grew from %d to %d rows on an auto-completing step", before, after)
	}
}
