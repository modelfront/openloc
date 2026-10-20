package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// GET /api/translations/{id}/history — the read surface over
// translation_history that the CAT editor sidebar's History section renders.
//
// The emission side is covered by handler_emission_integration_test.go; these
// tests are about the read: ordering, the LEFT joins that resolve engine / TM /
// user / step names, what the payload deliberately omits, and tenant isolation.

// historyEvent mirrors the handler's response shape. Only the fields the
// assertions touch — decoding into a typed struct would hide a field the
// handler stopped sending, so TestHistoryOmitsEditingTime decodes into a map.
type historyEvent struct {
	Kind           string  `json:"kind"`
	Text           *string `json:"text"`
	TMMatchPercent *int16  `json:"tm_match_percent"`
	EditDistance   *int32  `json:"edit_distance"`
	CreatedByName  *string `json:"created_by_name"`
	CreatedByEmail *string `json:"created_by_email"`
	MTEngineName   *string `json:"mt_engine_name"`
	TMName         *string `json:"tm_name"`
	StepName       *string `json:"step_name"`
	AssignmentID   *int64  `json:"job_step_assignment_id"`
	Details        struct {
		PredictOutcome string `json:"predict_outcome"`
	} `json:"details"`
}

type historyFixture struct {
	q        db.Querier
	router   http.Handler
	userID   int64
	tenantID int64
	fileID   int64
	trID     int64
	engineID int64
	tmID     int64
	// The job's in-progress step assignment, which the confirm is attributed to.
	assignmentID int64
}

// seedHistoryChain builds one segment carrying a realistic lineage:
// file_import → tm (82%, "Acme DE") → mt (DeepL, predict rejected) → human.
// The human row comes from a real HTTP confirm so the read test exercises at
// least one genuinely-emitted row rather than four hand-inserted ones.
func seedHistoryChain(t *testing.T) historyFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "segment-history")
	proj := testutil.SeedProject(t, q, tenantID, "Segment History", "en", []string{"es"})

	file := testutil.SeedFile(t, q, proj.ID, "history.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Welcome home", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")
	assignmentID := testutil.SeedJobStep(t, q, proj.SettingsProfileID, job.ID, &userID)

	tm := testutil.SeedTM(t, q, tenantID, "Acme DE")
	engine := testutil.SeedMTEngine(t, q, tenantID, "DeepL", "deepl")

	imported := "Bienvenido"
	tmHit := "Bienvenido a casa"
	mtOut := "Bienvenido a casa."
	pct := int16(82)
	// The MT phase is what leaves text on the parent row, and the confirm below
	// measures its edit distance against it. Emission only stamps the lineage
	// columns, so this write is separate — as it is in the real pipeline.
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{
		ID: tr.ID, ActiveText: &mtOut,
	}); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}

	// Emitted through the pipeline helper rather than raw inserts so the lineage
	// columns on translations move with the events, as they do in production.
	for _, opts := range []pipeline.AutomationEmitOpts{
		{TranslationID: tr.ID, Kind: db.TranslationHistoryKindFileImport, Origin: db.TranslationOriginFileImport, Text: &imported},
		{TranslationID: tr.ID, Kind: db.TranslationHistoryKindTm, Origin: db.TranslationOriginTm, Text: &tmHit, TmID: &tm.ID, TmMatchPercent: &pct},
		{TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt, Origin: db.TranslationOriginMt, Text: &mtOut, MtEngineID: &engine.ID,
			Details: map[string]any{"predict_outcome": "rejected"}},
	} {
		if err := pipeline.EmitAutomationHistoryInTx(ctx, q, opts); err != nil {
			t.Fatalf("seed %s history: %v", opts.Kind, err)
		}
	}

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Bienvenido a casa, señor."}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	return historyFixture{
		q: q, router: router,
		userID: userID, tenantID: tenantID,
		fileID: file.ID, trID: tr.ID, engineID: engine.ID, tmID: tm.ID,
		assignmentID: assignmentID,
	}
}

func derefInt32(v *int32) any {
	if v == nil {
		return nil
	}
	return *v
}

func getHistory(t *testing.T, f historyFixture, userID, tenantID int64) []historyEvent {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "GET",
		fmt.Sprintf("/api/translations/%d/history", f.trID), nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var body struct {
		Data []historyEvent `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	return body.Data
}

func TestIntegrationHistory_OrderAndJoins(t *testing.T) {
	f := seedHistoryChain(t)
	events := getHistory(t, f, f.userID, f.tenantID)

	if len(events) != 4 {
		t.Fatalf("want 4 history events, got %d", len(events))
	}

	// Newest first. The three seeded rows share a transaction-free insert order
	// but distinct timestamps; the human confirm is last in and so first out.
	wantKinds := []string{"human", "mt", "tm", "file_import"}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d: want kind %q, got %q", i, want, events[i].Kind)
		}
	}

	human, mt, tm := events[0], events[1], events[2]

	if human.Text == nil || *human.Text != "Bienvenido a casa, señor." {
		t.Errorf("human.text = %v, want the confirmed text", human.Text)
	}
	if human.CreatedByName == nil || *human.CreatedByName == "" {
		t.Error("human.created_by_name: want the confirming user's name, got nil/empty")
	}
	if human.CreatedByEmail == nil || *human.CreatedByEmail == "" {
		t.Error("human.created_by_email: want the confirming user's email, got nil/empty")
	}
	// Attributed to the job's in-progress step so the panel can show where the
	// edit happened.
	if human.StepName == nil || *human.StepName != "translate" {
		t.Errorf("human.step_name = %v, want \"translate\"", human.StepName)
	}
	// Levenshtein against the pre-edit active_text, which the MT row left set.
	if human.EditDistance == nil || *human.EditDistance == 0 {
		t.Errorf("human.edit_distance = %v, want a non-zero distance from the MT output", derefInt32(human.EditDistance))
	}

	if mt.MTEngineName == nil || *mt.MTEngineName != "DeepL" {
		t.Errorf("mt.mt_engine_name = %v, want \"DeepL\"", mt.MTEngineName)
	}
	if mt.Details.PredictOutcome != "rejected" {
		t.Errorf("mt.details.predict_outcome = %q, want \"rejected\"", mt.Details.PredictOutcome)
	}

	if tm.TMName == nil || *tm.TMName != "Acme DE" {
		t.Errorf("tm.tm_name = %v, want \"Acme DE\"", tm.TMName)
	}
	if tm.TMMatchPercent == nil || *tm.TMMatchPercent != 82 {
		t.Errorf("tm.tm_match_percent = %v, want 82", tm.TMMatchPercent)
	}
}

// The panel collapses one person's saves in one step into a row, keyed on the
// step assignment rather than the step name — two adjacent steps can share a
// name. Pre-translation runs outside any step and carries none.
func TestIntegrationHistory_CarriesStepAssignment(t *testing.T) {
	f := seedHistoryChain(t)
	events := getHistory(t, f, f.userID, f.tenantID)
	if len(events) != 4 {
		t.Fatalf("want 4 history events, got %d", len(events))
	}

	if got := events[0].AssignmentID; got == nil || *got != f.assignmentID {
		t.Errorf("human.job_step_assignment_id = %v, want %d", got, f.assignmentID)
	}
	for _, e := range events[1:] {
		if e.AssignmentID != nil {
			t.Errorf("%s.job_step_assignment_id = %d, want null — pre-translation runs outside a step", e.Kind, *e.AssignmentID)
		}
	}
}

// Every provenance FK is ON DELETE SET NULL, so retiring an engine must leave
// the events it produced readable — losing the audit because someone cleaned up
// an unused engine would defeat the point of an append-only log.
func TestIntegrationHistory_SurvivesDeletedEngine(t *testing.T) {
	f := seedHistoryChain(t)

	if err := f.q.SoftDeleteMTEngine(context.Background(), db.SoftDeleteMTEngineParams{
		ID: f.engineID, TenantID: f.tenantID,
	}); err != nil {
		t.Fatalf("delete engine: %v", err)
	}

	events := getHistory(t, f, f.userID, f.tenantID)
	if len(events) != 4 {
		t.Fatalf("want 4 events after engine delete, got %d", len(events))
	}
	mt := events[1]
	if mt.Kind != "mt" {
		t.Fatalf("event 1: want kind mt, got %q", mt.Kind)
	}
	if mt.MTEngineName != nil {
		t.Errorf("mt.mt_engine_name = %v, want nil after the engine was deleted", mt.MTEngineName)
	}
	if mt.Text == nil || *mt.Text == "" {
		t.Error("mt.text: the event's own payload must survive the engine delete")
	}
}

// editing_time_ms exists on the table for the per-step productivity report. The
// panel deliberately doesn't serve it: a per-segment timer on a colleague's
// work answers a management question this surface doesn't ask. Pinned here so
// re-adding the column to the response is a conscious choice, not a drive-by
// "the row struct has it, might as well".
func TestIntegrationHistory_OmitsEditingTime(t *testing.T) {
	f := seedHistoryChain(t)

	rec := testutil.DoRequest(t, f.router, "GET",
		fmt.Sprintf("/api/translations/%d/history", f.trID), nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var body struct {
		Data []map[string]any `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if len(body.Data) == 0 {
		t.Fatal("no events returned")
	}
	for i, ev := range body.Data {
		if _, present := ev["editing_time_ms"]; present {
			t.Errorf("event %d exposes editing_time_ms; the panel must not serve it", i)
		}
	}
}

// translation_history has no tenant_id of its own, so the handler resolves the
// parent translation tenant-scoped first. A neighbouring tenant must get a 404,
// not someone else's segment audit.
func TestIntegrationHistory_CrossTenantIsNotFound(t *testing.T) {
	f := seedHistoryChain(t)
	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "segment-history-intruder")

	rec := testutil.DoRequest(t, f.router, "GET",
		fmt.Sprintf("/api/translations/%d/history", f.trID), nil, otherUser, otherTenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// Edit distance on a confirm must measure the human's change against what the
// machine produced — the point of a post-edit metric.
//
// The trap: a draft autosave writes the typed text into active_text and emits
// no history row. A confirm that measures against active_text is therefore
// comparing the new text with itself and records 0. The baseline must be the
// newest history row carrying text, not the segment's current text.
func TestIntegrationHistory_EditDistanceSurvivesDraftAutosave(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "edit-distance")
	proj := testutil.SeedProject(t, q, tenantID, "Edit Distance", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "dist.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Meaning of grizzly", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	// What MT produced, both on the row and as a history event.
	const mtOut = `Significado de "grizzly"`
	const confirmed = `Significado de "grizzly" edited`
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{
		ID: tr.ID, ActiveText: ptr.To(mtOut),
	}); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	if err := pipeline.EmitAutomationHistoryInTx(ctx, q, pipeline.AutomationEmitOpts{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt,
		Origin: db.TranslationOriginMt, Text: ptr.To(mtOut),
	}); err != nil {
		t.Fatalf("seed MT history: %v", err)
	}

	// The user types: autosave persists the draft, silently, with no history.
	draft := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
		map[string]any{"human_text": confirmed}, userID, tenantID)
	testutil.AssertStatus(t, draft, http.StatusOK)

	// Then confirms.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": confirmed}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	want := int32(len(" edited")) // pure insertion at the end

	var got *int32
	if err := pool.QueryRow(ctx, `
		SELECT edit_distance FROM translation_history
		WHERE translation_id = $1 AND kind = 'human'
		ORDER BY id DESC LIMIT 1`, tr.ID).Scan(&got); err != nil {
		t.Fatalf("read history edit_distance: %v", err)
	}
	if got == nil || *got != want {
		t.Errorf("history edit_distance = %v, want %d (distance from the MT output)", derefInt32(got), want)
	}

	// The parent column feeds the productivity report and must agree.
	var onRow *int32
	if err := pool.QueryRow(ctx,
		`SELECT edit_distance FROM translations WHERE id = $1`, tr.ID).Scan(&onRow); err != nil {
		t.Fatalf("read translations.edit_distance: %v", err)
	}
	if onRow == nil || *onRow != want {
		t.Errorf("translations.edit_distance = %v, want %d", derefInt32(onRow), want)
	}
}

// A Cmd+Enter that changes nothing is an approval, not an edit: it records
// `approved` with no text, so the panel does not show it as an edit and it
// never serves as the next Δ's baseline.
func TestIntegrationHistory_ConfirmWithoutChangeRecordsApproval(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "approve-noop")
	proj := testutil.SeedProject(t, q, tenantID, "Approve Noop", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "noop.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Meaning of grizzly", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	const mtOut = `Significado de "grizzly"`
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: tr.ID, ActiveText: ptr.To(mtOut)}); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	if err := pipeline.EmitAutomationHistoryInTx(ctx, q, pipeline.AutomationEmitOpts{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt,
		Origin: db.TranslationOriginMt, Text: ptr.To(mtOut),
	}); err != nil {
		t.Fatalf("seed MT history: %v", err)
	}

	// Confirm the MT exactly as it stands.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": mtOut}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var kind string
	var text *string
	var dist *int32
	var createdBy *int64
	if err := pool.QueryRow(ctx, `
		SELECT kind, text, edit_distance, created_by FROM translation_history
		WHERE translation_id = $1 ORDER BY id DESC LIMIT 1`, tr.ID).
		Scan(&kind, &text, &dist, &createdBy); err != nil {
		t.Fatalf("read newest history: %v", err)
	}
	if kind != "approved" {
		t.Errorf("newest kind = %q, want \"approved\"", kind)
	}
	if text != nil {
		t.Errorf("approved.text = %v, want NULL — a status event carries no text", *text)
	}
	if dist != nil {
		t.Errorf("approved.edit_distance = %v, want NULL — nothing was edited", *dist)
	}
	if createdBy == nil || *createdBy != userID {
		t.Errorf("approved.created_by = %v, want the confirming user", derefInt64(createdBy))
	}

	var humans int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM translation_history WHERE translation_id = $1 AND kind = 'human'`,
		tr.ID).Scan(&humans); err != nil {
		t.Fatalf("count human rows: %v", err)
	}
	if humans != 0 {
		t.Errorf("got %d human rows, want 0 — the confirm changed nothing", humans)
	}
}

// The status rows carry no text precisely so they cannot become the baseline
// GetLatestHistoryTextForTranslation returns. If one ever did, the next real
// edit would measure against it and Δ would collapse.
func TestIntegrationHistory_ApprovalDoesNotBecomeEditDistanceBaseline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "approve-baseline")
	proj := testutil.SeedProject(t, q, tenantID, "Approve Baseline", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "baseline.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Meaning of grizzly", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	const mtOut = `Significado de "grizzly"`
	const edited = `Significado de "grizzly" edited`
	if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: tr.ID, ActiveText: ptr.To(mtOut)}); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	if err := pipeline.EmitAutomationHistoryInTx(ctx, q, pipeline.AutomationEmitOpts{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt,
		Origin: db.TranslationOriginMt, Text: ptr.To(mtOut),
	}); err != nil {
		t.Fatalf("seed MT history: %v", err)
	}

	confirm := func(text string) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "PUT",
			fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
			map[string]any{"human_text": text}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	confirm(mtOut) // approval, no text recorded
	confirm(edited)

	want := int32(len(" edited"))
	var got *int32
	if err := pool.QueryRow(ctx, `
		SELECT edit_distance FROM translation_history
		WHERE translation_id = $1 AND kind = 'human' ORDER BY id DESC LIMIT 1`, tr.ID).Scan(&got); err != nil {
		t.Fatalf("read edit_distance: %v", err)
	}
	if got == nil || *got != want {
		t.Errorf("edit_distance = %v, want %d — measured from the MT text, skipping the approval row",
			derefInt32(got), want)
	}
}

// Losing approval is recorded on the transition. The autosave fires every few
// seconds while someone types; only the save that actually moves the status off
// `approved` writes a row.
func TestIntegrationHistory_DraftRecordsUnapprovalOnce(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "unconfirm")
	proj := testutil.SeedProject(t, q, tenantID, "Unconfirm", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "unconfirm.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Meaning of grizzly", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	confirmURL := fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID)
	draftURL := confirmURL + "/draft"

	rec := testutil.DoRequest(t, router, "PUT", confirmURL,
		map[string]any{"human_text": "Aprobado"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	countUnconfirmed := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM translation_history WHERE translation_id = $1 AND kind = 'unconfirmed'`,
			tr.ID).Scan(&n); err != nil {
			t.Fatalf("count unconfirmed: %v", err)
		}
		return n
	}

	if n := countUnconfirmed(); n != 0 {
		t.Fatalf("before editing: %d unconfirmed rows, want 0", n)
	}

	// First autosave after the confirm — this is the transition.
	rec = testutil.DoRequest(t, router, "PUT", draftURL,
		map[string]any{"human_text": "Aprobado y"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if n := countUnconfirmed(); n != 1 {
		t.Fatalf("after the first draft save: %d unconfirmed rows, want 1", n)
	}

	// Typing continues. The segment is already needs_review, so nothing more
	// is recorded however many times the debounce fires.
	for i, text := range []string{"Aprobado y e", "Aprobado y ed", "Aprobado y edit"} {
		rec = testutil.DoRequest(t, router, "PUT", draftURL,
			map[string]any{"human_text": text}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if n := countUnconfirmed(); n != 1 {
			t.Fatalf("after draft save %d: %d unconfirmed rows, want 1 — the status had already dropped", i+2, n)
		}
	}

	// Drafts still write no text.
	var textRows int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM translation_history
		WHERE translation_id = $1 AND kind = 'unconfirmed' AND text IS NOT NULL`, tr.ID).Scan(&textRows); err != nil {
		t.Fatalf("count unconfirmed rows with text: %v", err)
	}
	if textRows != 0 {
		t.Errorf("%d unconfirmed rows carry text, want 0", textRows)
	}
}

func derefInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// TestIntegrationHistory_LinguistNeedsAssignment pins the route's
// `RequireLinguistTranslationAccess` gate. The other history tests all drive a
// non-linguist, and the middleware waves those through untouched — so without
// this test the middleware can be deleted from the route with the suite still
// green, and every linguist gains read access to any segment's history,
// including who edited it, their email and the text at each step.
//
// Same shape as the file-download and job-stop pins: one request that must
// reach the handler, one that must not.
func TestIntegrationHistory_LinguistNeedsAssignment(t *testing.T) {
	f := seedHistoryChain(t)

	// The fixture assigns its user to the job's step, so as a linguist they are
	// assigned and clear the gate.
	rec := testutil.DoRequestWithRole(t, f.router, "GET",
		fmt.Sprintf("/api/translations/%d/history", f.trID), nil, f.userID, f.tenantID, "linguist")
	if rec.Code != http.StatusOK {
		t.Errorf("assigned linguist must reach the handler, got %d: %s", rec.Code, rec.Body.String())
	}

	// Same tenant, no assignment anywhere near this file.
	outsider := testutil.SeedUserWithRole(t, f.q, f.tenantID, "history-outsider@test.com", "pass", "linguist")
	rec = testutil.DoRequestWithRole(t, f.router, "GET",
		fmt.Sprintf("/api/translations/%d/history", f.trID), nil, outsider.ID, f.tenantID, "linguist")
	if rec.Code != http.StatusForbidden {
		t.Errorf("unassigned linguist must be blocked, got %d: %s", rec.Code, rec.Body.String())
	}
}
