// Phase-construction tests for the file-upload hook. Pins the contract:
// settings flags (mt_on_upload, tm_on_upload) → pipeline.TranslatePhases
// → asynq EnqueuePipeline payload. Catches the class of bug where the gate
// (`if enqueuer != nil && ...`) or the phases struct (`UseTM/UseMT`) drifts
// out of sync with the settings flags.
//
// Why this lives at the file-package level: the upload hook in
// internal/file/handler.go reads settings and constructs phases. The pure
// pipeline tests in internal/pipeline/translate_phases_integration_test.go
// prove the pipeline does the right thing given any phases input — but they
// don't exercise the settings→phases mapping that the upload hook owns.
package file_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// recordingEnqueuer captures every EnqueuePipeline call so tests can assert
// what the upload hook tried to schedule.
type recordingEnqueuer struct {
	mu    sync.Mutex
	calls []enqueueCall
}

type enqueueCall struct {
	FileID      int64
	SourceLang  string
	TargetLang  string
	JobID       int64
	TenantID    int64
	Phases      pipeline.TranslatePhases
	TriggeredBy *int64
}

func (e *recordingEnqueuer) EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, enqueueCall{fileID, sourceLang, targetLang, jobID, tenantID, phases, triggeredBy})
	return nil
}

// EnqueueAnalysis is a no-op stub so this recorder satisfies the wider
// api.TaskEnqueuer interface (which the test router type expects).
// Saved-analyses-specific assertions live in internal/analysis tests; this
// recorder doesn't need to capture them.
func (e *recordingEnqueuer) EnqueueAnalysis(analysisID, tenantID int64) error { return nil }

func (e *recordingEnqueuer) snapshot() []enqueueCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]enqueueCall, len(e.calls))
	copy(out, e.calls)
	return out
}

// uploadWithSettings is the test scaffold: seeds project + settings + uploads a
// JSON i18n file, returns the recorded enqueue calls. Each test sets the three
// flags to a specific combo and asserts the resulting Phases.
func uploadWithSettings(t *testing.T, mtOnUpload, tmOnUpload bool) []enqueueCall {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	rec := &recordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, rec)

	tenant := testutil.SeedTenant(t, q, "UploadPhases")
	user := testutil.SeedUser(t, q, tenant.ID, "u@uploadphases.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "UploadPhasesProject", "en", []string{"fr"})

	if _, err := q.UpdateSettingsProfile(context.Background(), db.UpdateSettingsProfileParams{
		ID:             project.SettingsProfileID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     mtOnUpload,
		TmOnUpload:     tmOnUpload,
	}); err != nil {
		t.Fatalf("UpdateSettingsProfile: %v", err)
	}

	httpRec := testutil.DoFileUpload(t, router,
		fmt.Sprintf("/api/projects/%d/files", project.ID),
		"upload.json", "application/json", `{"hello": "Hello world"}`,
		user.ID, tenant.ID)
	testutil.AssertStatus(t, httpRec, http.StatusCreated)

	return rec.snapshot()
}

// Anti-tautology RED proofs for the settings→phases mapping (2026-05-15):
//
//   Mutation 1: revert the gate to `if h.enqueuer != nil && settingsProfile.MtOnUpload`.
//     Diagnostic: TM-only case (pre=false, tm=true) records 0 calls instead of 1.
//                 The "TM only on upload enqueues with UseTM/no UseMT" test fails.
//
//   Mutation 2: revert phase construction to `{UseTM: tm, UseMT: true}` (hardcode).
//     Diagnostic: the MT-only case shape is unchanged but the TM-only case
//                 records `Phases{UseTM: true, UseMT: true}` — full pipeline —
//                 instead of TM-only. The Phases-shape assertion fails.

func TestIntegrationUploadHookEnqueuesPhases_TMOnly(t *testing.T) {
	calls := uploadWithSettings(t, false, true)
	if len(calls) != 1 {
		t.Fatalf("TM-only: want 1 enqueue, got %d", len(calls))
	}
	if want := (pipeline.TranslatePhases{UseTM: true, UseMT: false}); !reflect.DeepEqual(calls[0].Phases, want) {
		t.Errorf("TM-only: phases = %+v, want %+v", calls[0].Phases, want)
	}
}

func TestIntegrationUploadHookEnqueuesPhases_MTOnly(t *testing.T) {
	calls := uploadWithSettings(t, true, false)
	if len(calls) != 1 {
		t.Fatalf("MT-only: want 1 enqueue, got %d", len(calls))
	}
	if want := (pipeline.TranslatePhases{UseTM: false, UseMT: true}); !reflect.DeepEqual(calls[0].Phases, want) {
		t.Errorf("MT-only: phases = %+v, want %+v", calls[0].Phases, want)
	}
}

// QP+APE runs as a QE+APE workflow step, not an upload phase, so
// "both on" is the full pipeline (TM + MT).
func TestIntegrationUploadHookEnqueuesPhases_BothOn(t *testing.T) {
	calls := uploadWithSettings(t, true, true)
	if len(calls) != 1 {
		t.Fatalf("Both-on: want 1 enqueue, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].Phases, pipeline.FullPipeline) {
		t.Errorf("Both-on: phases = %+v, want FullPipeline", calls[0].Phases)
	}
}

func TestIntegrationUploadHookEnqueuesPhases_AllOffSkipsEnqueue(t *testing.T) {
	calls := uploadWithSettings(t, false, false)
	if len(calls) != 0 {
		t.Errorf("All-off: want 0 enqueues, got %d (%+v)", len(calls), calls)
	}
}
