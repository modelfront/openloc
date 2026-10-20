package translation_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A TM match or MT suggestion inserted from the editor's Resources panel is
// saved as a draft carrying the panel's insert token. The server records the
// TM / MT event from the token, so the Match column shows it exactly as it
// does after pre-translation.

type panelFixture struct {
	pool             *pgxpool.Pool
	q                db.Querier
	router           chi.Router
	userID, tenantID int64
	project          db.Project
	file             db.File
	job              db.Job
}

func newPanelFixture(t *testing.T, name string) *panelFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	tenant := testutil.SeedTenant(t, q, name)
	user := testutil.SeedUser(t, q, tenant.ID, strings.ToLower(name)+"@panel.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, name+" project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	return &panelFixture{
		pool: pool, q: q, router: testutil.NewTestRouter(pool),
		userID: user.ID, tenantID: tenant.ID, project: project, file: file, job: job,
	}
}

// segment seeds one untranslated segment of the fixture's file.
func (f *panelFixture) segment(t *testing.T, source string, pos int32) db.Translation {
	t.Helper()
	seg := testutil.SeedSegment(t, f.q, f.file.ID, source, pos)
	return testutil.SeedTranslation(t, f.q, seg.ID, "es", db.TranslationStatusNeedsReview)
}

// tmEntry stores one en→es entry in tm.
func (f *panelFixture) tmEntry(t *testing.T, tm db.TmCollection, src, tgt string) {
	t.Helper()
	if _, err := f.q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: src, TargetText: tgt, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
}

// entryID is the id of the only entry in tm.
func (f *panelFixture) entryID(t *testing.T, tm db.TmCollection) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM translation_memory WHERE tm_id = $1`, tm.ID).Scan(&id); err != nil {
		t.Fatalf("entry of TM %d: %v", tm.ID, err)
	}
	return id
}

func (f *panelFixture) assignedTM(t *testing.T) db.TmCollection {
	t.Helper()
	tm := testutil.SeedTM(t, f.q, f.tenantID, "Panel TM")
	if _, err := f.q.CreateSettingsTMAssignment(context.Background(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: f.project.SettingsProfileID, TmID: tm.ID, Mode: "read_write",
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	return tm
}

type panelTMMatch struct {
	Score       float64 `json:"score"`
	Percent     int16   `json:"percent"`
	SourceText  string  `json:"source_text"`
	TargetText  string  `json:"target_text"`
	TMID        int64   `json:"tm_id"`
	InsertToken string  `json:"insert_token"`
}

func (f *panelFixture) tmMatches(t *testing.T, source string) []panelTMMatch {
	t.Helper()
	return f.tmMatchesFor(t, f.job.ID, source, 0)
}

// tmMatchesFor is the panel's lookup for one segment; translationID 0 sends
// the source text alone.
func (f *panelFixture) tmMatchesFor(t *testing.T, jobID int64, source string, translationID int64) []panelTMMatch {
	t.Helper()
	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", jobID, url.QueryEscape(source))
	if translationID > 0 {
		path += fmt.Sprintf("&translation_id=%d", translationID)
	}
	rec := testutil.DoRequest(t, f.router, "GET", path, nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Matches []panelTMMatch `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Matches) == 0 {
		t.Fatalf("no TM matches for %q", source)
	}
	return resp.Matches
}

type panelMT struct {
	Text        string `json:"text"`
	Engine      string `json:"engine"`
	InsertToken string `json:"insert_token"`
}

func (f *panelFixture) mtPreview(t *testing.T, source string, translationID int64) panelMT {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "GET",
		fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=%s&translation_id=%d", f.job.ID, url.QueryEscape(source), translationID),
		nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp panelMT
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Text == "" {
		t.Fatalf("empty MT preview for %q", source)
	}
	return resp
}

// draft PUTs a draft save and returns the response.
func (f *panelFixture) draft(t *testing.T, tr db.Translation, text, token string, expected time.Time) (int, db.Translation, string) {
	t.Helper()
	body := map[string]any{"human_text": text, "expected_updated_at": expected}
	if token != "" {
		body["suggestion"] = token
	}
	rec := testutil.DoRequest(t, f.router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", f.file.ID, tr.ID), body, f.userID, f.tenantID)
	var resp struct {
		Data db.Translation `json:"data"`
	}
	if rec.Code == http.StatusOK {
		testutil.DecodeJSON(t, rec, &resp)
	}
	return rec.Code, resp.Data, rec.Body.String()
}

type lineage struct {
	origin    *string
	percent   *int16
	updatedAt time.Time
}

func (f *panelFixture) lineage(t *testing.T, id int64) lineage {
	t.Helper()
	var l lineage
	if err := f.pool.QueryRow(context.Background(),
		`SELECT origin::text, tm_match_percent, updated_at FROM translations WHERE id = $1`, id,
	).Scan(&l.origin, &l.percent, &l.updatedAt); err != nil {
		t.Fatalf("read lineage: %v", err)
	}
	return l
}

type historyRow struct {
	kind      string
	text      *string
	createdBy *int64
	tmID      *int64
	entryID   *int64
	engineID  *int64
	aiEngine  *int64
	percent   *int16
	source    *string
}

func (f *panelFixture) history(t *testing.T, id int64) []historyRow {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `
		SELECT kind::text, text, created_by, tm_id, tm_entry_id, mt_engine_id, ai_engine_id, tm_match_percent, details->>'source'
		FROM translation_history WHERE translation_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	defer rows.Close()
	var out []historyRow
	for rows.Next() {
		var h historyRow
		if err := rows.Scan(&h.kind, &h.text, &h.createdBy, &h.tmID, &h.entryID, &h.engineID, &h.aiEngine, &h.percent, &h.source); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		out = append(out, h)
	}
	return out
}

func orNil(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestIntegrationPanelInsert_TMMatch(t *testing.T) {
	f := newPanelFixture(t, "PanelInsertTM")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello, world", "Hola, mundo")
	tr := f.segment(t, "Hello world", 1)

	m := f.tmMatches(t, "Hello world")[0]
	if m.Score >= 1 {
		t.Fatalf("fixture must be a fuzzy match, got score %v", m.Score)
	}
	if m.InsertToken == "" {
		t.Fatal("TM match carries no insert_token")
	}
	wantPercent := int16(math.Round(m.Score * 100))

	code, saved, body := f.draft(t, tr, m.TargetText, m.InsertToken, tr.UpdatedAt.Time)
	if code != http.StatusOK {
		t.Fatalf("draft with TM token: %d %s", code, body)
	}

	got := f.lineage(t, tr.ID)
	if orNil(got.origin) != "tm" {
		t.Errorf("origin = %s, want tm", orNil(got.origin))
	}
	if got.percent == nil || *got.percent != wantPercent {
		t.Errorf("tm_match_percent = %v, want %d (the panel's %v)", got.percent, wantPercent, m.Score)
	}
	// The response is the row as it stands after the lineage write, so the
	// editor's next save carries a version that still matches.
	if !saved.UpdatedAt.Time.Equal(got.updatedAt) {
		t.Errorf("response updated_at %v, row has %v", saved.UpdatedAt.Time, got.updatedAt)
	}
	if saved.Origin.TranslationOrigin != db.TranslationOriginTm || saved.TmMatchPercent == nil || *saved.TmMatchPercent != wantPercent {
		t.Errorf("response lineage = %v / %v, want tm / %d", saved.Origin, saved.TmMatchPercent, wantPercent)
	}

	hist := f.history(t, tr.ID)
	if len(hist) != 1 || hist[0].kind != "tm" {
		t.Fatalf("history = %+v, want one tm row", hist)
	}
	h := hist[0]
	if orNil(h.source) != "panel_insert" || orNil(h.text) != m.TargetText ||
		h.tmID == nil || *h.tmID != tm.ID || h.entryID == nil || *h.entryID != f.entryID(t, tm) ||
		h.createdBy == nil || *h.createdBy != f.userID ||
		h.percent == nil || *h.percent != wantPercent {
		t.Errorf("tm history row = %+v", h)
	}

	// The translator keeps editing: the next draft saves cleanly and the
	// match stays on the row.
	code, _, body = f.draft(t, tr, m.TargetText+" ya", "", saved.UpdatedAt.Time)
	if code != http.StatusOK {
		t.Fatalf("follow-up draft: %d %s", code, body)
	}
	if after := f.lineage(t, tr.ID); orNil(after.origin) != "tm" || after.percent == nil || *after.percent != wantPercent {
		t.Errorf("after an edit lineage = %s / %v, want tm / %d", orNil(after.origin), after.percent, wantPercent)
	}
}

func TestIntegrationPanelInsert_MTSuggestion(t *testing.T) {
	f := newPanelFixture(t, "PanelInsertMT")
	engine := testutil.SeedMTEngine(t, f.q, f.tenantID, "Mock MT", "mock")
	testutil.SeedMTRouterForProfile(t, f.q, f.tenantID, f.project.SettingsProfileID, &engine.ID, nil)
	tr := f.segment(t, "Good morning", 1)

	mt := f.mtPreview(t, "Good morning", tr.ID)
	if mt.InsertToken == "" {
		t.Fatal("MT suggestion carries no insert_token")
	}
	code, saved, body := f.draft(t, tr, mt.Text, mt.InsertToken, tr.UpdatedAt.Time)
	if code != http.StatusOK {
		t.Fatalf("draft with MT token: %d %s", code, body)
	}

	got := f.lineage(t, tr.ID)
	if orNil(got.origin) != "mt" || got.percent != nil {
		t.Errorf("lineage = %s / %v, want mt / nil", orNil(got.origin), got.percent)
	}
	if !saved.UpdatedAt.Time.Equal(got.updatedAt) {
		t.Errorf("response updated_at %v, row has %v", saved.UpdatedAt.Time, got.updatedAt)
	}
	hist := f.history(t, tr.ID)
	if len(hist) != 1 || hist[0].kind != "mt" {
		t.Fatalf("history = %+v, want one mt row", hist)
	}
	if h := hist[0]; orNil(h.source) != "panel_insert" || h.engineID == nil || *h.engineID != engine.ID || orNil(h.text) != mt.Text {
		t.Errorf("mt history row = %+v", h)
	}

	// The stored suggestion (now on record) inserts the same way, so a TM
	// insert in between can be undone by picking the MT again.
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Good morning!", "¡Buenos días!")
	m := f.tmMatches(t, "Good morning")[0]
	code, saved, body = f.draft(t, tr, m.TargetText, m.InsertToken, saved.UpdatedAt.Time)
	if code != http.StatusOK {
		t.Fatalf("draft with TM token: %d %s", code, body)
	}
	stored := f.mtPreview(t, "Good morning", tr.ID)
	if code, _, body = f.draft(t, tr, stored.Text, stored.InsertToken, saved.UpdatedAt.Time); code != http.StatusOK {
		t.Fatalf("draft with stored MT token: %d %s", code, body)
	}
	if got := f.lineage(t, tr.ID); orNil(got.origin) != "mt" || got.percent != nil {
		t.Errorf("after re-inserting MT lineage = %s / %v, want mt / nil", orNil(got.origin), got.percent)
	}
	hist = f.history(t, tr.ID)
	if h := hist[len(hist)-1]; h.kind != "mt" || orNil(h.source) != "panel_insert" || h.engineID == nil || *h.engineID != engine.ID {
		t.Errorf("stored MT re-insert history row = %+v, want mt / panel_insert / engine %d", h, engine.ID)
	}
}

// The usual stored case: pre-translation recorded the MT with its engine, and
// inserting that suggestion records the same engine, vendor or AI alike.
func TestIntegrationPanelInsert_StoredMTKeepsItsEngine(t *testing.T) {
	f := newPanelFixture(t, "PanelInsertStored")
	ctx := context.Background()
	vendor := testutil.SeedMTEngine(t, f.q, f.tenantID, "Vendor MT", "mock")
	ai, err := f.q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: f.tenantID, Name: "LLM MT", Purpose: "mt"})
	if err != nil {
		t.Fatalf("CreateAIEngine: %v", err)
	}

	cases := []struct {
		name       string
		source     string
		mtID, aiID *int64
	}{
		{"vendor engine", "Good evening", &vendor.ID, nil},
		{"AI engine", "Good night", nil, &ai.ID},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := f.segment(t, c.source, int32(i+1))
			text := "[ES] " + c.source
			if _, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
				TranslationID: tr.ID, Kind: db.TranslationHistoryKindMt, Text: &text,
				MtEngineID: c.mtID, AiEngineID: c.aiID, Details: []byte(`{"engine_name":"stored"}`),
			}); err != nil {
				t.Fatalf("seed mt history: %v", err)
			}
			mt := f.mtPreview(t, c.source, tr.ID)
			if mt.Text != text {
				t.Fatalf("preview = %q, want the stored %q", mt.Text, text)
			}
			if code, _, body := f.draft(t, tr, mt.Text, mt.InsertToken, tr.UpdatedAt.Time); code != http.StatusOK {
				t.Fatalf("draft with stored MT token: %d %s", code, body)
			}
			hist := f.history(t, tr.ID)
			h := hist[len(hist)-1]
			if h.kind != "mt" || orNil(h.source) != "panel_insert" {
				t.Fatalf("newest history row = %+v, want an mt panel insert", h)
			}
			if !sameID(h.engineID, c.mtID) || !sameID(h.aiEngine, c.aiID) {
				t.Errorf("insert attributed mt=%v ai=%v, want mt=%v ai=%v", h.engineID, h.aiEngine, c.mtID, c.aiID)
			}
		})
	}
}

func sameID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// A token proves where a suggestion came from only for the segment it was
// issued for. Anything else is refused and writes nothing.
func TestIntegrationPanelInsert_RefusesForeignToken(t *testing.T) {
	f := newPanelFixture(t, "PanelInsertRefuse")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello, world", "Hola, mundo")
	tr := f.segment(t, "Hello world", 1)
	other := f.segment(t, "Something else entirely", 2)
	token := f.tmMatches(t, "Hello world")[0].InsertToken

	// Same source text in another job of the tenant.
	otherFile := testutil.SeedFile(t, f.q, f.project.ID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, otherFile.ID, "es")
	otherSeg := testutil.SeedSegment(t, f.q, otherFile.ID, "Hello world", 1)
	otherTr := testutil.SeedTranslation(t, f.q, otherSeg.ID, "es", db.TranslationStatusNeedsReview)

	mid := len(token) / 2
	flipped := byte('A')
	if token[mid] == 'A' {
		flipped = 'B'
	}
	tampered := token[:mid] + string(flipped) + token[mid+1:]

	cases := []struct {
		name   string
		fileID int64
		tr     db.Translation
		token  string
	}{
		{"another segment of the job", f.file.ID, other, token},
		{"the same source in another job", otherFile.ID, otherTr, token},
		{"a tampered token", f.file.ID, tr, tampered},
		{"not a token", f.file.ID, tr, "garbage"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, f.router, "PUT",
				fmt.Sprintf("/api/files/%d/translations/%d/draft", c.fileID, c.tr.ID),
				map[string]any{"human_text": "Hola, mundo", "suggestion": c.token}, f.userID, f.tenantID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			var text *string
			if err := f.pool.QueryRow(context.Background(), `SELECT active_text FROM translations WHERE id = $1`, c.tr.ID).Scan(&text); err != nil {
				t.Fatal(err)
			}
			if text != nil {
				t.Errorf("refused draft still wrote %q", *text)
			}
			if got := f.lineage(t, c.tr.ID); got.origin != nil {
				t.Errorf("refused draft stamped origin %s", *got.origin)
			}
			if h := f.history(t, c.tr.ID); len(h) != 0 {
				t.Errorf("refused draft wrote history %+v", h)
			}
		})
	}
}

// A TM entry deleted after the panel showed it cannot be recorded: the draft
// answers 409 and rolls back, so the text is not saved without its lineage.
func TestIntegrationPanelInsert_DeletedEntry(t *testing.T) {
	f := newPanelFixture(t, "PanelInsertGone")
	tm := f.assignedTM(t)
	f.tmEntry(t, tm, "Hello, world", "Hola, mundo")
	tr := f.segment(t, "Hello world", 1)
	m := f.tmMatches(t, "Hello world")[0]

	ctx := context.Background()
	entryID := f.entryID(t, tm)
	if n, err := f.q.DeleteTMEntry(ctx, db.DeleteTMEntryParams{ID: entryID, TmID: tm.ID}); err != nil || n != 1 {
		t.Fatalf("DeleteTMEntry: %d, %v", n, err)
	}

	code, _, body := f.draft(t, tr, m.TargetText, m.InsertToken, tr.UpdatedAt.Time)
	if code != http.StatusConflict {
		t.Fatalf("draft with a deleted entry: %d %s, want 409", code, body)
	}
	// Its own code, so the editor tells it apart from a version conflict.
	if !strings.Contains(body, `"code":"suggestion_gone"`) {
		t.Errorf("409 body %s carries no suggestion_gone code", body)
	}
	var text *string
	if err := f.pool.QueryRow(ctx, `SELECT active_text FROM translations WHERE id = $1`, tr.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != nil {
		t.Errorf("rolled-back draft still wrote %q", *text)
	}
	if h := f.history(t, tr.ID); len(h) != 0 {
		t.Errorf("rolled-back draft wrote history %+v", h)
	}
}
