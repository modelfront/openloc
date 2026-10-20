package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// setContext stores the neighbour context of a TM entry, as a confirm writes
// it: the md5 of the previous and next source segments.
func (f *panelFixture) setContext(t *testing.T, tm db.TmCollection, source, prev, next string) {
	t.Helper()
	tag, err := f.pool.Exec(context.Background(), `
		UPDATE translation_memory SET prev_source_hash = md5($3), next_source_hash = md5($4)
		WHERE tm_id = $1 AND source_text = $2`, tm.ID, source, prev, next)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("set context of %q: %v (%d rows)", source, err, tag.RowsAffected())
	}
}

// The panel scores an in-context exact match 101%, as pre-translation does,
// once it knows which segment it is looking up for — and inserting it keeps
// the 101%.
func TestIntegrationPanelInsert_InContextMatch(t *testing.T) {
	f := newPanelFixture(t, "PanelContext")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello world", "Hola mundo")
	f.setContext(t, tm, "Hello world", "First line.", "Last line.")

	f.segment(t, "First line.", 1)
	tr := f.segment(t, "Hello world", 2)
	f.segment(t, "Last line.", 3)

	// Another file of the project, where the same sentence has other neighbours.
	otherFile := testutil.SeedFile(t, f.q, f.project.ID, "other.txt", db.FileFormatTxt)
	otherJob := testutil.SeedJob(t, f.q, otherFile.ID, "es")
	testutil.SeedSegment(t, f.q, otherFile.ID, "Something else.", 1)
	otherSeg := testutil.SeedSegment(t, f.q, otherFile.ID, "Hello world", 2)
	otherTr := testutil.SeedTranslation(t, f.q, otherSeg.ID, "es", db.TranslationStatusNeedsReview)

	if got := f.tmMatchesFor(t, f.job.ID, "Hello world", tr.ID)[0].Percent; got != 101 {
		t.Errorf("in context: percent %d, want 101", got)
	}
	if got := f.tmMatchesFor(t, otherJob.ID, "Hello world", otherTr.ID)[0].Percent; got != 100 {
		t.Errorf("other neighbours: percent %d, want 100", got)
	}
	if got := f.tmMatchesFor(t, f.job.ID, "Hello world", 0)[0].Percent; got != 100 {
		t.Errorf("no segment given: percent %d, want 100", got)
	}

	m := f.tmMatchesFor(t, f.job.ID, "Hello world", tr.ID)[0]

	// A repetition of the sentence elsewhere in the file has other
	// neighbours: the 101% token of the first one does not verify on it.
	repeat := f.segment(t, "Hello world", 4)
	if code, _, body := f.draft(t, repeat, m.TargetText, m.InsertToken, repeat.UpdatedAt.Time); code != http.StatusBadRequest {
		t.Errorf("101%% token on a repetition with other neighbours: %d %s, want 400", code, body)
	}

	code, saved, body := f.draft(t, tr, m.TargetText, m.InsertToken, tr.UpdatedAt.Time)
	if code != http.StatusOK {
		t.Fatalf("draft with the 101%% match: %d %s", code, body)
	}
	if saved.TmMatchPercent == nil || *saved.TmMatchPercent != 101 {
		t.Errorf("inserted in-context match stored %v, want 101", saved.TmMatchPercent)
	}
}

// The panel takes a segment's neighbours only from a segment of the job it
// looks up for, with the source it looks up. Each segment below sits between
// the stored neighbours, so trusting it would show 101%.
func TestIntegrationPanelInsert_ContextIgnoresForeignSegments(t *testing.T) {
	f := newPanelFixture(t, "PanelContextScope")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello world", "Hola mundo")
	f.setContext(t, tm, "Hello world", "First line.", "Last line.")

	inContext := func(fileID int64, source string, at int32) db.Translation {
		testutil.SeedSegment(t, f.q, fileID, "First line.", at)
		seg := testutil.SeedSegment(t, f.q, fileID, source, at+1)
		testutil.SeedSegment(t, f.q, fileID, "Last line.", at+2)
		return testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview)
	}

	// The control: this job's own segment in context.
	own := inContext(f.file.ID, "Hello world", 1)
	if got := f.tmMatchesFor(t, f.job.ID, "Hello world", own.ID)[0].Percent; got != 101 {
		t.Fatalf("own segment in context: percent %d, want 101", got)
	}

	otherFile := testutil.SeedFile(t, f.q, f.project.ID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, otherFile.ID, "es")
	otherJob := inContext(otherFile.ID, "Hello world", 1)

	otherTenant := testutil.SeedTenant(t, f.q, "PanelContextScopeOther")
	otherProject := testutil.SeedProject(t, f.q, otherTenant.ID, "Other tenant project", "en", []string{"es"})
	otherTenantFile := testutil.SeedFile(t, f.q, otherProject.ID, "doc.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, otherTenantFile.ID, "es")
	otherTenantTr := inContext(otherTenantFile.ID, "Hello world", 1)

	sameJobOtherSource := inContext(f.file.ID, "Goodbye world", 11)

	for name, tr := range map[string]db.Translation{
		"another job":              otherJob,
		"another tenant":           otherTenantTr,
		"this job, another source": sameJobOtherSource,
	} {
		if got := f.tmMatchesFor(t, f.job.ID, "Hello world", tr.ID)[0].Percent; got != 100 {
			t.Errorf("%s: percent %d, want 100", name, got)
		}
	}
}

// For every segment, the panel's top match carries the percentage that
// pre-translation stores in the Match column: one TM, so both pick the same
// entry, and every scoring rule is exercised once.
func TestIntegrationPanelMatchesPretranslation(t *testing.T) {
	f := newPanelFixture(t, "PanelParity")
	ctx := context.Background()
	tm := testutil.SeedTM(t, f.q, f.tenantID, "Parity TM")
	if _, err := f.q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: f.project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", PenaltyPct: 0,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	segments := []string{
		"Welcome aboard.",         // exact, no stored context: 100
		"Hello world",             // exact in context: 101
		"Thanks for waiting",      // exact, other context: 100
		"Please sign in",          // near-exact (trailing period): 99
		"Reset your password now", // fuzzy
		"Good morning",            // exact, sublanguage variant: penalised
	}
	entries := []struct{ src, tgt, tgtLang string }{
		{"Welcome aboard.", "Bienvenido a bordo.", "es"},
		{"Hello world", "Hola mundo", "es"},
		{"Thanks for waiting", "Gracias por esperar", "es"},
		{"Please sign in.", "Inicie sesión.", "es"},
		{"Reset your password", "Restablezca su contraseña", "es"},
		{"Good morning", "Buenos días", "es-mx"},
	}
	for _, e := range entries {
		if _, err := f.q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: e.tgtLang,
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM %q: %v", e.src, err)
		}
	}
	f.setContext(t, tm, "Hello world", "Welcome aboard.", "Thanks for waiting")
	f.setContext(t, tm, "Thanks for waiting", "Somewhere else", "Nowhere")

	trs := make([]db.Translation, len(segments))
	for i, src := range segments {
		trs[i] = f.segment(t, src, int32(i+1))
	}

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/tm-apply", f.job.ID), nil, f.userID, f.tenantID)
	if rec.Code >= 300 {
		t.Fatalf("tm-apply: %d %s", rec.Code, rec.Body.String())
	}

	seen := map[int16]bool{}
	for i, src := range segments {
		stored := f.lineage(t, trs[i].ID)
		if orNil(stored.origin) != "tm" || stored.percent == nil {
			t.Fatalf("%q: pre-translation left %s / %v, want a TM match", src, orNil(stored.origin), stored.percent)
		}
		top := f.tmMatchesFor(t, f.job.ID, src, trs[i].ID)[0]
		if top.Percent != *stored.percent {
			t.Errorf("%q: panel shows %d%%, Match column %d%%", src, top.Percent, *stored.percent)
		}
		seen[*stored.percent] = true
	}
	// The fixture is only a parity test if it spans the rules.
	for _, want := range []int16{101, 100, 99} {
		if !seen[want] {
			t.Errorf("no segment pre-translated at %d%%: the fixture no longer covers that rule (got %v)", want, seen)
		}
	}
}

// A token binds the exact source text it was scored for: a segment whose
// source differs only by surrounding spaces must not take a 100% token issued
// for the trimmed text, since pre-translation would not score it 100%.
func TestIntegrationPanelInsert_TokenBindsExactSource(t *testing.T) {
	f := newPanelFixture(t, "PanelExactSource")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello world", "Hola mundo")
	padded := f.segment(t, " Hello world", 1)

	m := f.tmMatchesFor(t, f.job.ID, "Hello world", 0)[0]
	if m.Percent != 100 {
		t.Fatalf("fixture: trimmed lookup percent %d, want 100", m.Percent)
	}
	if code, _, body := f.draft(t, padded, m.TargetText, m.InsertToken, padded.UpdatedAt.Time); code != http.StatusBadRequest {
		t.Errorf("100%% token for the trimmed text on %q: %d %s, want 400", " Hello world", code, body)
	}

	// The MT preview trims what it sends to the engine, but its token still
	// binds the segment's exact source, so its own suggestion inserts.
	engine := testutil.SeedMTEngine(t, f.q, f.tenantID, "Mock MT", "mock")
	testutil.SeedMTRouterForProfile(t, f.q, f.tenantID, f.project.SettingsProfileID, &engine.ID, nil)
	spaced := f.segment(t, " Good morning ", 2)
	mt := f.mtPreview(t, " Good morning ", spaced.ID)
	if code, _, body := f.draft(t, spaced, mt.Text, mt.InsertToken, spaced.UpdatedAt.Time); code != http.StatusOK {
		t.Errorf("MT token for its own spaced segment: %d %s, want 200", code, body)
	}
}
