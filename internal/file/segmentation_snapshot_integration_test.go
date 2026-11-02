package file_test

// A file is split with its project's segmentation profile at upload and keeps
// a copy of those rules: every later parse (publish of a draft, export) splits
// it the same way, whatever the project's profile is by then. Profile A breaks
// only after "!", profile B after "." and "!", so the same text yields
// different sentence keys under each.

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/pipeline"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

const snapshotText = "One. Two! Three."

var (
	setA = segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `!`, After: `\s`, Break: true}}}
	setB = segmentation.RuleSet{Rules: []segmentation.Rule{{Before: `[.!]`, After: `\s`, Break: true}}}
)

type snapshotFixture struct {
	pool   *pgxpool.Pool
	q      db.Querier
	router http.Handler
	store  *file.MemoryStore
	svc    *segmentationapi.Service
	tenant db.Tenant
	owner  db.User
	// workflow is the library workflow its drafts name, so they may publish.
	workflow int64
}

func newSnapshotFixture(t *testing.T, name string) snapshotFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	store := file.NewMemoryStore()
	tenant := testutil.SeedTenant(t, q, name)
	return snapshotFixture{
		pool: pool, q: q, store: store, tenant: tenant,
		router:   testutil.NewTestRouterWithStorage(pool, &recordingEnqueuer{}, store),
		svc:      segmentationapi.NewService(q, pool),
		owner:    testutil.SeedUser(t, q, tenant.ID, "owner@"+name+".test", "pass"),
		workflow: testutil.LibraryWorkflow(t, q, tenant.ID),
	}
}

// profile stores rs as an org profile for lang.
func (f snapshotFixture) profile(t *testing.T, name, lang string, rs segmentation.RuleSet) int64 {
	t.Helper()
	p, err := f.svc.Create(context.Background(), f.tenant.ID, f.owner.ID, segmentationapi.Input{Name: name, SourceLanguages: []string{lang}, Definition: &rs})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// useProfile makes a project split its next uploads with a profile, through
// PATCH /settings.
func (f snapshotFixture) useProfile(t *testing.T, projectID, profileID int64) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"segmentation_profile_id": profileID}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// draft creates a draft project in lang that names profileID.
func (f snapshotFixture) draft(t *testing.T, lang string, profileID int64) int64 {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", "/api/projects", map[string]any{
		"status": "draft", "name": "Draft", "source_language": lang, "target_languages": []string{"fr"}, "workflow_id": f.workflow,
		"settings": map[string]any{"mt_on_upload": false, "tm_on_upload": false, "segmentation_profile_id": profileID},
	}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	return uploadID(t, rec)
}

// projectProfile is the segmentation profile a project names.
func (f snapshotFixture) projectProfile(t *testing.T, projectID int64) int64 {
	t.Helper()
	var id *int64
	if err := f.pool.QueryRow(context.Background(), `SELECT sp.segmentation_profile_id FROM projects p
		JOIN settings_profiles sp ON sp.id = p.settings_profile_id WHERE p.id = $1`, projectID).Scan(&id); err != nil || id == nil {
		t.Fatalf("project %d profile = %v (%v)", projectID, id, err)
	}
	return *id
}

func (f snapshotFixture) upload(t *testing.T, projectID int64, name string) int64 {
	t.Helper()
	return f.uploadText(t, projectID, name, snapshotText)
}

func (f snapshotFixture) uploadText(t *testing.T, projectID int64, name, text string) int64 {
	t.Helper()
	return f.uploadAs(t, projectID, name, "text/plain", text)
}

func (f snapshotFixture) uploadAs(t *testing.T, projectID int64, name, contentType, text string) int64 {
	t.Helper()
	rec := testutil.DoFileUpload(t, f.router, fmt.Sprintf("/api/projects/%d/files", projectID),
		name, contentType, text, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	return uploadID(t, rec)
}

func (f snapshotFixture) sources(t *testing.T, fileID int64) []string {
	t.Helper()
	segs, err := f.q.ListSegmentsByFile(context.Background(), fileID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range segs {
		out = append(out, s.SourceText)
	}
	return out
}

// export translates every segment of fileID as "FR:<source>" and exports it.
func (f snapshotFixture) export(t *testing.T, fileID int64) string {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE translations t SET active_text = 'FR:' || s.source_text
		FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1`, fileID); err != nil {
		t.Fatal(err)
	}
	out, _, err := pipeline.NewService(f.q, pipeline.NewPgxTxRunner(f.pool), f.store).ExportFile(ctx, f.tenant.ID, fileID, "fr")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestIntegrationSegmentation_UploadUsesTheProjectsProfileAndExportKeepsIt(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotLive")
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Live", "en", []string{"fr"})
	a, b := f.profile(t, "A", "en", setA), f.profile(t, "B", "en", setB)

	f.useProfile(t, project.ID, a)
	fileA := f.upload(t, project.ID, "a.txt")
	if got, want := f.sources(t, fileA), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("segments under profile A = %q, want %q", got, want)
	}

	f.useProfile(t, project.ID, b)
	fileB := f.upload(t, project.ID, "b.txt")
	if got, want := f.sources(t, fileB), []string{"One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("segments under profile B = %q, want %q", got, want)
	}

	// File A exports with A's keys although the project uses B now.
	if got, want := f.export(t, fileA), "FR:One. Two! FR:Three."; got != want {
		t.Errorf("export of the file split with A = %q, want %q", got, want)
	}
	if got, want := f.export(t, fileB), "FR:One. FR:Two! FR:Three."; got != want {
		t.Errorf("export of the file split with B = %q, want %q", got, want)
	}

	// Deleting the profiles changes nothing for the files that hold their
	// snapshots.
	for _, id := range []int64{a, b} {
		if err := f.svc.Delete(context.Background(), f.tenant.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := f.export(t, fileA), "FR:One. Two! FR:Three."; got != want {
		t.Errorf("export after the profiles were deleted = %q, want %q", got, want)
	}
}

// A draft publishes its files with the rules stored at upload, even when its
// profile's rules changed since.
func TestIntegrationSegmentation_DraftPublishKeepsTheUploadSnapshot(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotDraft")
	ctx := context.Background()
	a := f.profile(t, "A", "en", setA)
	draftID := f.draft(t, "en", a)
	fileID := f.upload(t, draftID, "a.txt")

	cur, err := f.svc.Get(ctx, f.tenant.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	rs := setB
	if _, err := f.svc.Update(ctx, f.tenant.ID, a, segmentationapi.Input{Name: "A", SourceLanguages: []string{"en"}, Definition: &rs, UpdatedAt: &cur.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", draftID),
		map[string]any{"analyze": false}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Publish re-parses the stored source; with the edited rules the segments
	// would no longer match and be replaced by three.
	if got, want := f.sources(t, fileID), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("segments after publish = %q, want the upload's %q", got, want)
	}
	if got, want := f.export(t, fileID), "FR:One. Two! FR:Three."; got != want {
		t.Errorf("export = %q, want %q", got, want)
	}
}

// A draft whose profile changed since the upload publishes its files with
// the profile it names now.
func TestIntegrationSegmentation_DraftProfileChangeResolvesAgainAtPublish(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotDraftProfile")
	a, b := f.profile(t, "A", "en", setA), f.profile(t, "B", "en", setB)
	draftID := f.draft(t, "en", a)
	fileID := f.upload(t, draftID, "a.txt")

	rec := testutil.DoRequest(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/draft", draftID), map[string]any{
		"name": "Draft", "source_language": "en", "target_languages": []string{"fr"}, "workflow_id": f.workflow,
		"settings": map[string]any{"segmentation_profile_id": b},
	}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", draftID),
		map[string]any{"analyze": false}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got, want := f.sources(t, fileID), []string{"One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("segments after publish with B = %q, want %q", got, want)
	}
}

// A file stored without a snapshot keeps the
// conservative built-in set, not the project's profile of today.
func TestIntegrationSegmentation_FileWithoutSnapshotUsesConservative(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotNull")
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Old", "en", []string{"fr"})
	conservative, ok := segmentation.BuiltinByID(segmentation.ConservativeID)
	if !ok {
		t.Fatal("no conservative built-in")
	}
	// The conservative set ends a segment at the colon; the English built-in
	// and profile A do not, so only the conservative re-parse lines the keys
	// up.
	f.useProfile(t, project.ID, f.profile(t, "Conservative copy", "en", conservative.RuleSet))
	fileID := f.uploadText(t, project.ID, "old.txt", "Revised Information: F7m Specifications. Two!")
	ctx := context.Background()
	if got, want := f.sources(t, fileID), []string{"Revised Information:", "F7m Specifications.", "Two!"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upload with the conservative rules = %q, want %q", got, want)
	}

	// Stored without a snapshot.
	if _, err := f.pool.Exec(ctx, `UPDATE files SET segmentation = NULL WHERE id = $1`, fileID); err != nil {
		t.Fatal(err)
	}

	f.useProfile(t, project.ID, f.profile(t, "A", "en", setA))
	if got, want := f.export(t, fileID), "FR:Revised Information: FR:F7m Specifications. FR:Two!"; got != want {
		t.Errorf("export of a file without a snapshot = %q, want %q", got, want)
	}
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT coalesce(segmentation->'source'->>'kind', 'null') FROM files WHERE id = $1`, fileID).Scan(&kind); err != nil || kind != "null" {
		t.Errorf("snapshot after export = %q (%v), want still NULL", kind, err)
	}
}

// A draft's files are uploaded before its language may be final. A new
// language its profile does not suit moves the draft to the suggested
// profile for that language, and publishing splits its files with it.
func TestIntegrationSegmentation_DraftLanguageChangeResolvesAgainAtPublish(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotDraftLang")
	ctx := context.Background()
	draftID := f.draft(t, "en", f.profile(t, "English", "en", setA))
	fileID := f.upload(t, draftID, "a.txt")
	if got, want := f.sources(t, fileID), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("segments at upload = %q, want the English profile's %q", got, want)
	}

	rec := testutil.DoRequest(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/draft", draftID),
		map[string]any{"name": "Draft", "source_language": "de", "target_languages": []string{"fr"}, "workflow_id": f.workflow}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	german, err := segmentationapi.Suggest(ctx, f.q, "de")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.projectProfile(t, draftID); got != german {
		t.Errorf("profile after the language change = %d, want the German built-in %d", got, german)
	}
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", draftID),
		map[string]any{"analyze": false}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if got, want := f.sources(t, fileID), []string{"One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("segments after publish in German = %q, want the German built-in's %q", got, want)
	}
	if got, want := f.export(t, fileID), "FR:One. FR:Two! FR:Three."; got != want {
		t.Errorf("export = %q, want %q", got, want)
	}
}

// A profile whose rules would take too long on a file fails the upload with
// a message the user can act on, instead of running on.
func TestIntegrationSegmentation_TooCostlyRulesFailTheUpload(t *testing.T) {
	f := newSnapshotFixture(t, "SegTooCostly")
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Costly", "en", []string{"fr"})
	f.useProfile(t, project.ID, f.profile(t, "Costly", "en", testutil.CostlySegmentationRules()))
	rec := testutil.DoFileUpload(t, f.router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"long.txt", "text/plain", testutil.CostlySegmentationText, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "take too long to run on this file") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// HTML and XML exports split with the file's rules too, not the project's
// profile of today.
func TestIntegrationSegmentation_HTMLAndXMLExportWithTheUploadRules(t *testing.T) {
	f := newSnapshotFixture(t, "SegSnapshotMarkup")
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Markup", "en", []string{"fr"})
	a, b := f.profile(t, "A", "en", setA), f.profile(t, "B", "en", setB)
	for _, tc := range []struct{ name, contentType, content, want string }{
		{"page.html", "text/html", "<html><body><p>" + snapshotText + "</p></body></html>", "<p>FR:One. Two! FR:Three.</p>"},
		{"doc.xml", "application/xml", "<doc><p>" + snapshotText + "</p></doc>", "<doc><p>FR:One. Two! FR:Three.</p></doc>"},
	} {
		f.useProfile(t, project.ID, a)
		fileID := f.uploadAs(t, project.ID, tc.name, tc.contentType, tc.content)
		if got, want := f.sources(t, fileID), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s segments = %q, want profile A's %q", tc.name, got, want)
		}
		f.useProfile(t, project.ID, b)
		if got := f.export(t, fileID); !strings.Contains(got, tc.want) {
			t.Errorf("%s export = %q, want it to hold %q", tc.name, got, tc.want)
		}
	}
}

// Publishing a draft whose new profile has rules that take too long on a
// file fails with the import message, not a server error.
func TestIntegrationSegmentation_TooCostlyRulesFailThePublish(t *testing.T) {
	f := newSnapshotFixture(t, "SegTooCostlyPublish")
	ctx := context.Background()
	costly := f.profile(t, "Costly German", "de", testutil.CostlySegmentationRules())
	draftID := f.draft(t, "en", f.profile(t, "English", "en", setA))
	fileID := f.uploadText(t, draftID, "long.txt", testutil.CostlySegmentationText)
	before := f.sources(t, fileID)

	rec := testutil.DoRequest(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/draft", draftID), map[string]any{
		"name": "Draft", "source_language": "de", "target_languages": []string{"fr"}, "workflow_id": f.workflow,
		"settings": map[string]any{"segmentation_profile_id": costly},
	}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", draftID),
		map[string]any{"analyze": false}, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "take too long to run on this file") {
		t.Errorf("body = %s", rec.Body.String())
	}

	// The refused publish leaves the draft as it was.
	project, err := f.q.GetProject(ctx, db.GetProjectParams{ID: draftID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != db.ProjectStatusDraft {
		t.Errorf("project status = %q, want draft", project.Status)
	}
	var lang string
	if err := f.pool.QueryRow(ctx, `SELECT segmentation->>'language' FROM files WHERE id = $1`, fileID).Scan(&lang); err != nil || lang != "en" {
		t.Errorf("file snapshot language = %q (%v), want en", lang, err)
	}
	if got := f.sources(t, fileID); !reflect.DeepEqual(got, before) {
		t.Errorf("segments after the refused publish = %q, want %q", got, before)
	}
}

// Deleting a profile takes it off the projects that use it. Their uploads
// are refused, saying to pick another profile, until one is picked; the
// files already uploaded keep their rules.
func TestIntegrationSegmentation_DeletedProfileStopsUploadsUntilAnotherIsPicked(t *testing.T) {
	f := newSnapshotFixture(t, "SegDeleted")
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Uses it", "en", []string{"fr"})
	a, b := f.profile(t, "A", "en", setA), f.profile(t, "B", "en", setB)
	f.useProfile(t, project.ID, a)
	before := f.upload(t, project.ID, "before.txt")

	if err := f.svc.Delete(context.Background(), f.tenant.ID, a); err != nil {
		t.Fatal(err)
	}
	var profileID *int64
	if err := f.pool.QueryRow(context.Background(), `SELECT sp.segmentation_profile_id FROM projects p
		JOIN settings_profiles sp ON sp.id = p.settings_profile_id WHERE p.id = $1`, project.ID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if profileID != nil {
		t.Fatalf("project profile after the delete = %d, want none", *profileID)
	}
	rec := testutil.DoFileUpload(t, f.router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"after.txt", "text/plain", snapshotText, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "This project has no segmentation profile") {
		t.Errorf("upload without a profile: body = %s", rec.Body.String())
	}
	if got, want := f.sources(t, before), []string{"One. Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("file uploaded before the delete = %q, want its own rules' %q", got, want)
	}

	f.useProfile(t, project.ID, b)
	if got, want := f.sources(t, f.upload(t, project.ID, "picked.txt")), []string{"One.", "Two!", "Three."}; !reflect.DeepEqual(got, want) {
		t.Errorf("upload after picking B = %q, want %q", got, want)
	}
}
