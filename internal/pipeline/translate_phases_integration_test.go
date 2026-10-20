package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// =============================================================================
// TM/MT phase split — TranslatePhases gates which phases run
// =============================================================================
//
// Anti-tautology RED proofs (durable artifact)
//
//   Proof 1 — UseTM=true, UseMT=false skips the MT phase.
//     Mutation:   translate.go TranslateFile — change `if phases.UseMT {` to
//                 `if true {` (drop the UseMT gate).
//     Test:       TestIntegrationTranslateFile_TMOnly_LeavesNonMatchesUntranslated
//     Diagnostic: the un-TM-matched segment gets origin=mt, failing the
//                 nil-origin assertion.
//
//   Proof 2 — UseTM=false skips the TM phase entirely.
//     Mutation:   translate.go TranslateFile — change `if phases.UseTM {` to
//                 `if true {` (drop the UseTM gate).
//     Test:       TestIntegrationTranslateFile_MTOnly_SkipsTMEvenWhenItMatches
//     Diagnostic: the segment with a TM match gets origin=tm, failing the
//                 origin=mt assertion.
//
// =============================================================================

func TestIntegrationTranslateFile_TMOnly_LeavesNonMatchesUntranslated(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Two segments: one has a TM match, one doesn't.
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world", "Totally unique sentence."})

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile (TM-only): %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 translations, got %d", len(rows))
	}

	bySource := map[string]db.Translation{}
	for _, r := range rows {
		seg, _ := q.GetSegment(ctx, r.SegmentID)
		bySource[seg.SourceText] = r
	}

	// TM provenance is origin='tm'.
	tmHit := bySource["Hello world"]
	if !tmHit.Origin.Valid || tmHit.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("Hello world should be TM-matched (origin=tm); got %v", tmHit.Origin)
	}
	if tmHit.TmMatchPercent == nil || *tmHit.TmMatchPercent != 100 {
		t.Errorf("Hello world tm_match_percent: want 100, got %v", tmHit.TmMatchPercent)
	}

	unmatched := bySource["Totally unique sentence."]
	if unmatched.Status != db.TranslationStatusNeedsReview {
		t.Errorf("non-matched segment must stay untranslated when MT is disabled; got status=%s origin=%v", unmatched.Status, unmatched.Origin)
	}
	if unmatched.Origin.Valid {
		t.Errorf("non-matched segment must have nil origin; got %v", unmatched.Origin)
	}
}

// Apply TM after MT replaces MT-filled segments with the TM match; confirmed or
// human-edited segments are never overridden. ListTMTargetableByFile is what
// makes this work.
//
// Anti-tautology RED proof: revert internal/translation/queries.sql's TM phase
// to use ListUntranslatedByFile (instead of ListTMTargetableByFile) — the
// MT-filled segment stays `origin=mt`, the assertion fails.
func TestIntegrationTranslateFile_TMApply_OverridesMTFilledSegments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// 1) Apply MT first — segment becomes origin=mt, status=pre_translated.
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("MT phase: %v", err)
	}

	// 2) Apply TM after — TM-targetable set includes pre_translated rows; this
	//    segment flips to origin=tm with the TM target text.
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TM phase: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}
	tr := rows[0]
	// The override holds when the row started MT-filled, not just
	// untranslated: origin=tm, tm_match_percent=100, auto-approved.
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("override: expected origin=tm; got %v", tr.Origin)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Errorf("override: expected tm_match_percent=100; got %v", tr.TmMatchPercent)
	}
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Errorf("override: expected active_text='Bonjour le monde'; got %v", tr.ActiveText)
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("override: expected status=approved; got %s", tr.Status)
	}
}

// The TM override stops at confirmed/human-edited content. A segment that a
// user has touched (status promoted to approved by UpdateTranslationHuman)
// must NOT be overridden by Apply TM, even if TM has a matching entry.
//
// Anti-tautology RED proof: ListTMTargetableByFile guards human-edited rows
// by status (`t.status = 'needs_review'`) and by `t.edited_by IS NULL`. The
// seed leaves edited_by NULL, so status is the layer under test: widen the
// status filter to include `approved` and the row is re-targeted, the TM
// target overwrites the human text, and this assertion fails.
func TestIntegrationTranslateFile_TMApply_PreservesHumanEdited(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	// Pre-seed a human edit on the lone translation row. Mirrors what happens
	// after a translator confirms a segment in the CAT editor.
	existing, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(existing) != 1 {
		t.Fatalf("seed lookup: rows=%d err=%v", len(existing), err)
	}
	humanText := "Salut le monde — ma version"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        existing[0].ID,
		HumanText: &humanText,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman seed: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TM phase: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != humanText {
		t.Errorf("human_text must be preserved; got %v", tr.ActiveText)
	}
	// origin should NOT be 'tm' — the row was human-confirmed, so the TM
	// phase skips it (ListTMTargetableByFile) and never stamps the lineage.
	if tr.Origin.Valid && tr.Origin.TranslationOrigin == db.TranslationOriginTm {
		t.Errorf("origin must NOT flip to 'tm' when human_text is set; got %v", tr.Origin.TranslationOrigin)
	}
}

// User-reported scenario — linguist post-edits + confirms a segment
// (status='approved'), then Apply TM (or full Pre-translate) is re-run. The
// linguist's text MUST survive even when the TM has an exact match for the
// same source.
//
// Anti-tautology RED proof: drop the new `AND status = 'needs_review' AND
// edited_by IS NULL AND predict_outcome IS NULL` clause from
// UpdateTranslationTMFuzzy. The row gets re-targeted, the paired
// UpdateTranslationHuman overwrites the linguist's edits with the TM
// target, and the assertions below fail with the TM text shown.
func TestIntegrationTranslateFile_TMApply_PreservesApprovedSegments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde — TM version (should NOT win)",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	existing, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(existing) != 1 {
		t.Fatalf("seed lookup: rows=%d err=%v", len(existing), err)
	}
	linguist := testutil.SeedUser(t, q, tenantID, "linguist@inc116.test", "pw")
	linguistText := "Salut tout le monde — la version humaine (must survive)"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        existing[0].ID,
		HumanText: &linguistText,
		EditedBy:  &linguist.ID,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman seed: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("Apply TM re-run: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: rows=%d err=%v", len(rows), err)
	}
	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != linguistText {
		t.Errorf("linguist's approved text must survive Apply TM; got %v", tr.ActiveText)
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("status must stay 'approved' after Apply TM; got %s", tr.Status)
	}
}

// Race-window guard: even if a caller bypasses ListTMTargetableByFile
// and calls UpdateTranslationTMFuzzy directly on an approved row, the SQL
// WHERE clause must refuse the write. Simulates the race: a linguist
// confirms a segment between the pipeline's fetch and write.
//
// Anti-tautology RED proof: drop the new WHERE clause from
// UpdateTranslationTMFuzzy. The write succeeds, status flips to
// 'needs_review', and the Status assertion below fails.
func TestIntegrationTranslateFile_TMFuzzyWrite_GuardsApprovedAtSQLLevel(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})

	existing, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(existing) != 1 {
		t.Fatalf("seed lookup: rows=%d err=%v", len(existing), err)
	}
	linguist := testutil.SeedUser(t, q, tenantID, "guard@inc116.test", "pw")
	approvedText := "linguist confirmed this exact text"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        existing[0].ID,
		HumanText: &approvedText,
		EditedBy:  &linguist.ID,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman seed: %v", err)
	}

	// Simulate the race: try a TM fuzzy match write on the approved row.
	// The SQL guard must turn this into ErrNoRows.
	tmText := "TM target that MUST NOT win"
	_, err = q.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{
		ID:         existing[0].ID,
		ActiveText: &tmText,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from SQL guard; got %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("post lookup: rows=%d err=%v", len(rows), err)
	}
	if rows[0].ActiveText == nil || *rows[0].ActiveText != approvedText {
		t.Errorf("SQL guard must preserve approved text; got %v", rows[0].ActiveText)
	}
	if rows[0].Status != db.TranslationStatusApproved {
		t.Errorf("SQL guard must preserve approved status; got %s", rows[0].Status)
	}
}

// Race-window guard, MT side: same shape for UpdateTranslationMT.
// SQL clause: `active_text IS NULL AND edited_by IS NULL`.
//
// Anti-tautology RED proof: drop the new WHERE clause from
// UpdateTranslationMT. The write succeeds, status flips to needs_review,
// active_text becomes the MT text.
func TestIntegrationTranslateFile_MTWrite_GuardsApprovedAtSQLLevel(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})

	existing, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(existing) != 1 {
		t.Fatalf("seed lookup: rows=%d err=%v", len(existing), err)
	}
	linguist := testutil.SeedUser(t, q, tenantID, "mtguard@inc116.test", "pw")
	approvedText := "Salut tout le monde — humain"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        existing[0].ID,
		HumanText: &approvedText,
		EditedBy:  &linguist.ID,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman seed: %v", err)
	}

	mtText := "MT output — must NOT win"
	_, err = q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{
		ID:             existing[0].ID,
		ActiveText:     &mtText,
		ActiveRichText: nil,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from MT SQL guard; got %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("post lookup: rows=%d err=%v", len(rows), err)
	}
	if rows[0].ActiveText == nil || *rows[0].ActiveText != approvedText {
		t.Errorf("MT SQL guard must preserve approved text; got %v", rows[0].ActiveText)
	}
	if rows[0].Status != db.TranslationStatusApproved {
		t.Errorf("MT SQL guard must preserve approved status; got %s", rows[0].Status)
	}
}

func TestIntegrationTranslateFile_MTOnly_SkipsTMEvenWhenItMatches(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile (MT-only): %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	// The TM has an exact match for this segment, but UseTM=false must bypass it.
	// MT provenance is origin='mt'.
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("expected origin=mt (MT-only, TM phase skipped); got %v", tr.Origin)
	}
	if tr.TmMatchPercent != nil {
		t.Errorf("expected nil tm_match_percent (TM phase skipped); got %v", tr.TmMatchPercent)
	}
}

// "No MT router on this profile" must always mean "skip MT":
// resolveMTEngine returns nil, runMTPhase skips, segments stay untranslated.
// No construction-time fallback in production.
//
// Anti-tautology RED proof: make resolveMTEngine return a non-nil engine
// when the profile has no router (e.g. inject mt.Mock{} as the factory
// default) — the Mock runs, writes "[FR] Hello world", and the
// status==untranslated assertion fails.
func TestIntegrationTranslateFile_MTPhase_NoAssignment_SkipsMT(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})

	// Look up the project's settings profile so we can detach its MT router.
	f, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetFileTenant: %v", err)
	}
	project, err := q.GetProjectByID(ctx, f.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	testutil.SetProfileMTRouter(t, q, project.SettingsProfileID, nil)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile (MT, no assignment): %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation row, got %d", len(rows))
	}
	tr := rows[0]
	if tr.Status != db.TranslationStatusNeedsReview {
		t.Errorf("segment must stay untranslated when no MT engine is assigned; got status=%s active_text=%v origin=%v",
			tr.Status, tr.ActiveText, tr.Origin)
	}
	// Assert no automation lineage.
	if tr.Origin.Valid {
		t.Errorf("origin must be unset when MT phase was skipped; got %v (fallback to Mock?)", tr.Origin)
	}
	if tr.ActiveText != nil {
		t.Errorf("active_text must be nil when MT phase was skipped; got %q", *tr.ActiveText)
	}
}

// =============================================================================
// Pre-translate drops the QA findings of the rows it rewrote
// =============================================================================
//
// A stored finding describes one exact target text. TM and MT both replace that
// text without going through a QA run, so the finding — and the ignore that may
// sit on it — survives against content it was never computed on. The ignore is
// the sharper half: its fingerprint is the message, which
// derives from the old text, so it keeps suppressing a finding on text nobody
// looked at.
//
// Anti-tautology RED proof: drop the DeleteQAResultsForTranslations call from
// applyTMMatches (or translateMTChunks) in translate.go. The rewritten row keeps
// its seeded qa_results row and the length assertion fails. Verified 2026-09-09.

func seedQAFinding(t *testing.T, q db.Querier, translationID int64, checkType, message string) {
	t.Helper()
	if err := q.UpsertQAResult(context.Background(), db.UpsertQAResultParams{
		TranslationID: translationID,
		CheckType:     checkType,
		Severity:      "error",
		Message:       message,
	}); err != nil {
		t.Fatalf("seed qa_result(%d, %s): %v", translationID, checkType, err)
	}
}

func qaFindingCount(t *testing.T, q db.Querier, translationID int64) int {
	t.Helper()
	rows, err := q.ListQAResultsForTranslation(context.Background(), translationID)
	if err != nil {
		t.Fatalf("list qa_results(%d): %v", translationID, err)
	}
	return len(rows)
}

func onlyTranslation(t *testing.T, q db.Querier, fileID int64) db.Translation {
	t.Helper()
	rows, err := q.ListTranslationsByFile(context.Background(),
		db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}
	return rows[0]
}

func TestIntegrationTranslateFile_TMApply_DropsQAFindingsOfOverriddenRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("MT phase: %v", err)
	}
	tr := onlyTranslation(t, q, fileID)
	mtText := *tr.ActiveText
	seedQAFinding(t, q, tr.ID, "punctuation", "target ends with '!' but source does not")

	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TM phase: %v", err)
	}

	after := onlyTranslation(t, q, fileID)
	if after.ActiveText == nil || *after.ActiveText == mtText {
		t.Fatalf("precondition: TM must have overridden the MT text %q; got %v", mtText, after.ActiveText)
	}
	if n := qaFindingCount(t, q, after.ID); n != 0 {
		t.Errorf("want the overridden row's findings dropped, got %d still stored", n)
	}
}

func TestIntegrationTranslateFile_MTApply_DropsQAFindingsOfFilledRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// An empty segment can legitimately carry an `empty_target` finding from an
	// earlier QA run. MT fills it, so the finding no longer describes the row.
	tr := onlyTranslation(t, q, fileID)
	seedQAFinding(t, q, tr.ID, "empty_target", "target is empty")

	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("MT phase: %v", err)
	}

	after := onlyTranslation(t, q, fileID)
	if after.ActiveText == nil || *after.ActiveText == "" {
		t.Fatalf("precondition: MT must have filled the row; got %v", after.ActiveText)
	}
	if n := qaFindingCount(t, q, after.ID); n != 0 {
		t.Errorf("want the MT-filled row's findings dropped, got %d still stored", n)
	}
}

// The phases must not reach past the rows they wrote. An approved segment is
// excluded from both the TM-targetable and the untranslated sets, so its
// findings — and any ignore on them — outlive a re-run.
func TestIntegrationTranslateFile_Pretranslate_KeepsQAFindingsOfUntouchedRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	tr := onlyTranslation(t, q, fileID)
	humanText := "Salut le monde"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: tr.ID, HumanText: &humanText,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}
	seedQAFinding(t, q, tr.ID, "terminology", "term 'world' not translated as 'monde'")

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("full pipeline: %v", err)
	}

	after := onlyTranslation(t, q, fileID)
	if after.ActiveText == nil || *after.ActiveText != humanText {
		t.Fatalf("precondition: approved row must survive the re-run; got %v", after.ActiveText)
	}
	if n := qaFindingCount(t, q, after.ID); n != 1 {
		t.Errorf("want the untouched row's 1 finding kept, got %d", n)
	}
}

// Race-window guard for locks: a row someone locks after a phase listed it is
// refused by the write itself, MT and TM alike (an exact TM match would also
// approve it). Each write guard is pinned here on its own, since the phase row
// lists exclude locked rows too and would hide a missing guard.
func TestIntegrationTranslateFile_Writes_GuardLockedAtSQLLevel(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	_, fileID, _ := seedTranslateContext(t, q, []string{"Hello world"})
	existing, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(existing) != 1 {
		t.Fatalf("seed lookup: rows=%d err=%v", len(existing), err)
	}
	id := existing[0].ID
	if err := q.LockTranslation(ctx, id); err != nil {
		t.Fatal(err)
	}

	mtText := "MT output — must NOT land on a locked row"
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: id, ActiveText: &mtText}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("MT write on a locked row: want pgx.ErrNoRows, got %v", err)
	}
	tmText := "TM target — must NOT land on a locked row"
	if _, err := q.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{ID: id, ActiveText: &tmText}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("TM write on a locked row: want pgx.ErrNoRows, got %v", err)
	}

	got, err := q.GetTranslation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveText != nil || got.Status != db.TranslationStatusNeedsReview {
		t.Errorf("locked row changed: text %v, status %s", got.ActiveText, got.Status)
	}
}
