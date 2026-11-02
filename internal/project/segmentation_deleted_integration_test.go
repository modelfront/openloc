package project_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// holdDelete runs a profile delete's statements in a transaction the test
// commits: until then the profile row is locked, and a save that names it
// waits.
func (f profileFixture) holdDelete(id int64) pgx.Tx {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = tx.Rollback(ctx) }) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	if n, err := txq.SoftDeleteSegmentationProfile(ctx, db.SoftDeleteSegmentationProfileParams{ID: id, TenantID: f.tenantID}); err != nil || n != 1 {
		f.t.Fatalf("soft delete: %d rows, %v", n, err)
	}
	if _, err := txq.ClearSegmentationProfileFromProjects(ctx, db.ClearSegmentationProfileFromProjectsParams{TenantID: f.tenantID, ID: id}); err != nil {
		f.t.Fatal(err)
	}
	return tx
}

// saveDuringDelete sends a PATCH that names a profile while its delete is
// open, then commits the delete, and returns the response.
func (f profileFixture) saveDuringDelete(path string, profileID int64) *httptest.ResponseRecorder {
	f.t.Helper()
	tx := f.holdDelete(profileID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- testutil.DoRequest(f.t, f.router, "PATCH", path, map[string]any{"segmentation_profile_id": profileID}, f.userID, f.tenantID)
	}()
	testutil.WaitFor(f.t, func() bool { return len(done) == 1 || testutil.LockWaiters(f.t, f.pool) == 1 }, "the save to finish or wait on the delete")
	if err := tx.Commit(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	return <-done
}

// A save that names a profile while it is being deleted waits for the
// delete, is refused as for any deleted profile and keeps the profile it had.
func TestIntegrationSegmentationProfile_SaveDuringDelete(t *testing.T) {
	f := newProfileFixture(t, "SegSaveDuringDelete")
	code, projectID, before := f.createProjectFull("en", nil, 0)
	if code != http.StatusCreated {
		t.Fatalf("create project = %d", code)
	}
	rec := f.do("POST", "/api/templates", map[string]any{"name": "T", "source_language": "en"})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tmpl struct {
		Data struct {
			ID                    int64  `json:"id"`
			SegmentationProfileID *int64 `json:"segmentation_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmpl)

	for name, path := range map[string]string{
		"project settings": fmt.Sprintf("/api/projects/%d/settings", projectID),
		"template":         fmt.Sprintf("/api/templates/%d", tmpl.Data.ID),
	} {
		rec := f.saveDuringDelete(path, f.profile("Gone "+name, "en"))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Segmentation profile not found.") {
			t.Errorf("%s save during a delete = %d %s, want 400 profile not found", name, rec.Code, rec.Body.String())
		}
	}
	if got := f.projectProfile(projectID); got != before {
		t.Errorf("project profile = %d, want the one it had (%d)", got, before)
	}
	tp, err := f.q.GetTemplate(context.Background(), db.GetTemplateParams{ID: tmpl.Data.ID, TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := f.q.GetSettingsProfile(context.Background(), tp.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sp.SegmentationProfileID; got == nil || tmpl.Data.SegmentationProfileID == nil || *got != *tmpl.Data.SegmentationProfileID {
		t.Errorf("template profile = %s, want the one it had (%s)", testutil.Show(got), testutil.Show(tmpl.Data.SegmentationProfileID))
	}
}

// A template that still names a profile deleted after it was read passes on
// nothing: the project gets the suggested profile for its language, and a
// copy of the template's settings names none.
func TestIntegrationSegmentationProfile_TemplateOnADeletedProfile(t *testing.T) {
	f := newProfileFixture(t, "SegTemplateDeleted")
	ctx := context.Background()
	gone := f.profile("Gone", "en")
	tpl := testutil.SeedTemplate(t, f.q, f.tenantID, "T")
	if _, err := f.q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{ID: tpl.SettingsProfileID, SegmentationProfileID: &gone}); err != nil {
		t.Fatal(err)
	}
	// The state a delete racing the template's read leaves: the profile is
	// deleted, the template row was read before.
	if n, err := f.q.SoftDeleteSegmentationProfile(ctx, db.SoftDeleteSegmentationProfileParams{ID: gone, TenantID: f.tenantID}); err != nil || n != 1 {
		t.Fatalf("soft delete: %d rows, %v", n, err)
	}

	code, profile := f.createProject("en", nil, tpl.ID)
	if code != http.StatusCreated {
		t.Fatalf("create from the template = %d", code)
	}
	if want := f.suggested("en"); profile != want {
		t.Errorf("project profile = %d, want the suggested %d", profile, want)
	}

	src, err := f.q.GetSettingsProfile(ctx, tpl.SettingsProfileID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := settings.CreateProfileLike(ctx, f.q, f.tenantID, src)
	if err != nil {
		t.Fatalf("copy the template's settings: %v", err)
	}
	if copied.SegmentationProfileID != nil {
		t.Errorf("copy names profile %d, want none", *copied.SegmentationProfileID)
	}
}
