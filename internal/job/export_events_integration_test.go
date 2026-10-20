package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Audit: downloading a deliverable is a user-visible
// project action, so GET /api/jobs/{id}/export must land in the project
// history as a job.exported event with details.format distinguishing the
// final-file export ("source") from the bilingual XLIFF hand-off ("xliff").
//
// Only the xliff branch is exercised end-to-end here: the source-format
// branch downloads the original from S3, and the test router runs with nil
// Storage. Both branches share the same emitJobEvent one-liner; this test
// pins the mechanism (event type, format detail, snapshots, attribution).
func TestIntegrationExport_BilingualXLIFF_EmitsHistoryEvent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ExportEventsCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@export-events.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Export Events", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "manual.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// One keyed segment so the XLIFF serializer has a trans-unit to emit
	// (segment_key becomes the trans-unit id — see bilingual export tests).
	key := "line:1"
	if _, err := q.CreateSegment(ctx, db.CreateSegmentParams{
		FileID: file.ID, SourceText: "Hello", SegmentKey: &key, Position: 1,
	}); err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d/export?format=xliff", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	exported := testutil.ProjectEvents(t, q, project.ID, tenant.ID, "job.exported")
	if len(exported) != 1 {
		t.Fatalf("expected exactly 1 job.exported event, got %d", len(exported))
	}
	ev := exported[0]
	if ev.ActorID == nil || *ev.ActorID != user.ID {
		t.Errorf("job.exported must be attributed to the exporting user %d, got %v", user.ID, ev.ActorID)
	}
	if ev.JobID == nil || *ev.JobID != job.ID {
		t.Errorf("job.exported must carry the job id %d, got %v", job.ID, ev.JobID)
	}
	details := ev.Data
	if details["format"] != "xliff" {
		t.Errorf("details.format: want %q, got %v", "xliff", details["format"])
	}
	// Snapshots that keep the event renderable after file/job deletion.
	if details["file_name"] != "manual.txt" {
		t.Errorf("details.file_name: want %q, got %v", "manual.txt", details["file_name"])
	}
	if details["target_language"] != "de" {
		t.Errorf("details.target_language: want %q, got %v", "de", details["target_language"])
	}
}

// A rejected format never produced a download, so it must not pollute the
// history — the emit sits strictly on the success paths.
func TestIntegrationExport_BadFormat_EmitsNoEvent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ExportBadFormatCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@export-bad.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Export Bad Format", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "manual.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d/export?format=bogus", job.ID),
		nil, user.ID, tenant.ID,
	)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	if events := testutil.ProjectEvents(t, q, project.ID, tenant.ID, "job.exported"); len(events) != 0 {
		t.Fatalf("a 400 export must not emit job.exported, but found one: %s", string(events[0].Details))
	}
}
