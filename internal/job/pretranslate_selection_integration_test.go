package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// ptFixture: an en→de job with a mock MT engine and a TM holding
// "Good morning" → "Guten Morgen".
type ptFixture struct {
	pool   *pgxpool.Pool
	q      db.Querier
	router http.Handler
	tenant db.Tenant
	user   db.User
	file   db.File
	job    db.Job
	pos    int32
}

func newPTFixture(t *testing.T) *ptFixture {
	t.Helper()
	ctx := context.Background()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &ptFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}
	f.tenant = testutil.SeedTenant(t, q, "PTSelectionCo")
	f.user = testutil.SeedUser(t, q, f.tenant.ID, "u@pt-sel.test", "pass")
	project := testutil.SeedProject(t, q, f.tenant.ID, "PT Selection", "en", []string{"de"})
	engine := testutil.SeedMTEngine(t, q, f.tenant.ID, "Mock", "mock")
	testutil.SeedMTRouterForProfile(t, q, f.tenant.ID, project.SettingsProfileID, &engine.ID, nil)
	tm := testutil.SeedTM(t, q, f.tenant.ID, "PT TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Good morning", TargetText: "Guten Morgen", CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	f.file = testutil.SeedFile(t, q, project.ID, "pt.txt", db.FileFormatTxt)
	f.job = testutil.SeedJob(t, q, f.file.ID, "de")
	return f
}

// row seeds an empty de translation for a new segment.
func (f *ptFixture) row(t *testing.T, fileID int64, src string) int64 {
	t.Helper()
	f.pos++
	seg := testutil.SeedSegment(t, f.q, fileID, src, f.pos)
	return testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview).ID
}

func (f *ptFixture) lock(t *testing.T, id int64) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), "UPDATE translations SET locked = true WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
}

func (f *ptFixture) text(t *testing.T, id int64) (*string, db.TranslationStatus) {
	t.Helper()
	var text *string
	var st db.TranslationStatus
	if err := f.pool.QueryRow(context.Background(),
		"SELECT active_text, status FROM translations WHERE id = $1", id).Scan(&text, &st); err != nil {
		t.Fatal(err)
	}
	return text, st
}

func (f *ptFixture) apply(t *testing.T, kind string, body any) map[string]any {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/%s-apply", f.job.ID, kind), body, f.user.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Apply MT never writes a locked segment.
func TestIntegrationApplyMT_SkipsLocked(t *testing.T) {
	f := newPTFixture(t)
	open := f.row(t, f.file.ID, "Hello world")
	locked := f.row(t, f.file.ID, "Hello moon")
	f.lock(t, locked)

	f.apply(t, "mt", nil)
	if txt, _ := f.text(t, open); txt == nil {
		t.Error("unlocked row not filled by MT")
	}
	if txt, _ := f.text(t, locked); txt != nil {
		t.Errorf("locked row filled by MT: %q", *txt)
	}
}

// Apply TM never writes a locked segment — not even an exact match, which
// would also approve it.
func TestIntegrationApplyTM_SkipsLocked(t *testing.T) {
	f := newPTFixture(t)
	open := f.row(t, f.file.ID, "Good morning")
	locked := f.row(t, f.file.ID, "Good morning")
	f.lock(t, locked)

	f.apply(t, "tm", nil)
	if txt, _ := f.text(t, open); txt == nil || *txt != "Guten Morgen" {
		t.Errorf("unlocked row = %v, want the TM match", txt)
	}
	if txt, st := f.text(t, locked); txt != nil || st != db.TranslationStatusNeedsReview {
		t.Errorf("locked row written by TM: %v / %s", txt, st)
	}
}

// translation_ids limits Apply MT and Apply TM to those rows of the job; an
// id from another file is never touched.
func TestIntegrationApplyPhases_Selection(t *testing.T) {
	f := newPTFixture(t)
	selected := f.row(t, f.file.ID, "Hello world")
	other := f.row(t, f.file.ID, "Hello moon")
	tmSelected := f.row(t, f.file.ID, "Good morning")
	tmOther := f.row(t, f.file.ID, "Good morning")
	otherFile := testutil.SeedFile(t, f.q, f.file.ProjectID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, otherFile.ID, "de")
	foreign := f.row(t, otherFile.ID, "Hello sun")

	f.apply(t, "tm", map[string]any{"translation_ids": []int64{tmSelected, foreign}})
	if txt, _ := f.text(t, tmSelected); txt == nil {
		t.Error("selected row not filled by TM")
	}
	if txt, _ := f.text(t, tmOther); txt != nil {
		t.Errorf("unselected row filled by TM: %q", *txt)
	}

	f.apply(t, "mt", map[string]any{"translation_ids": []int64{selected, foreign}})
	if txt, _ := f.text(t, selected); txt == nil {
		t.Error("selected row not filled by MT")
	}
	for _, id := range []int64{other, foreign} {
		if txt, _ := f.text(t, id); txt != nil {
			t.Errorf("row %d outside the selection filled by MT: %q", id, *txt)
		}
	}
}

// A selection with nothing to fill answers no_segments and writes nothing; an
// empty id list is a bad request.
func TestIntegrationApplyPhases_SelectionEdges(t *testing.T) {
	f := newPTFixture(t)
	approved := f.row(t, f.file.ID, "Hello world")
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE translations SET active_text = 'Hallo Welt', status = 'approved' WHERE id = $1", approved); err != nil {
		t.Fatal(err)
	}
	out := f.apply(t, "mt", map[string]any{"translation_ids": []int64{approved}})
	if out["skip_reason"] != "no_segments" {
		t.Errorf("response = %v, want skip_reason no_segments", out)
	}
	if txt, _ := f.text(t, approved); txt == nil || *txt != "Hallo Welt" {
		t.Errorf("approved row changed: %v", txt)
	}

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", f.job.ID),
		map[string]any{"translation_ids": []int64{}}, f.user.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// Pre-translating a selection never completes the job's Pre-translation step:
// the rest of the job may still be empty. Here a Raw MT job's open step (left
// open by "no MT") stays open after Apply MT on one selected row.
func TestIntegrationApplyMT_SelectionKeepsPretranslationStepOpen(t *testing.T) {
	f := newPTFixture(t)
	ctx := context.Background()
	selected := f.row(t, f.file.ID, "Hello world")
	rest := f.row(t, f.file.ID, "Hello moon")
	project, err := f.q.GetProjectByID(ctx, f.file.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID, Name: "Pre-translation", StepOrder: 0,
		RoleRequired: "linguist", StepType: "pretranslate",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{JobID: f.job.ID, WorkflowStepID: ws.ID, Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: f.job.ID, CurrentStepID: &a.ID}); err != nil {
		t.Fatal(err)
	}

	f.apply(t, "mt", map[string]any{"translation_ids": []int64{selected}})
	if txt, _ := f.text(t, selected); txt == nil {
		t.Fatal("selected row not filled by MT")
	}
	if txt, _ := f.text(t, rest); txt != nil {
		t.Fatalf("unselected row filled: %q", *txt)
	}
	steps, err := f.q.ListJobStepAssignments(ctx, f.job.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListJobStepAssignments: %v (%d rows)", err, len(steps))
	}
	if steps[0].Status != "in_progress" {
		t.Errorf("Pre-translation step after a selection Apply MT = %q, want in_progress", steps[0].Status)
	}
	job, err := f.q.GetJob(ctx, f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status == "completed" {
		t.Error("job completed by a selection Apply MT")
	}
}
