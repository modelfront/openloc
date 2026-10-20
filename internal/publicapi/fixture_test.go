package publicapi_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

var roles = []string{"owner", "admin", "project_manager", "linguist"}

// fixture is one organization with a member of every role, each holding a
// read-and-write access key, and a project with one file and one job. The
// job's current step is assigned to the linguist.
type fixture struct {
	q      db.Querier
	router chi.Router
	client *v1Client

	tenant  db.Tenant
	users   map[string]db.User
	keys    map[string]string // role → read-and-write key
	readKey string            // the owner's read-only key

	project db.Project
	file    db.File
	job     db.Job
	// ids fills every other path parameter: one live item of each kind.
	ids map[string]int64
}

func newFixture(t *testing.T, q db.Querier, router chi.Router, name string) *fixture {
	t.Helper()
	f := &fixture{
		q: q, router: router, client: newV1Client(t, router),
		tenant: testutil.SeedTenant(t, q, name),
		users:  map[string]db.User{},
		keys:   map[string]string{},
	}
	for _, role := range roles {
		u := testutil.SeedUserWithRole(t, q, f.tenant.ID, fmt.Sprintf("%s@%s.example.com", role, strings.ToLower(name)), "pass", role)
		f.users[role] = u
		f.keys[role] = testutil.SeedAPIToken(t, q, f.tenant.ID, u.ID, "write", time.Hour)
	}
	f.readKey = testutil.SeedAPIToken(t, q, f.tenant.ID, f.users["owner"].ID, "read", time.Hour)

	f.project = testutil.SeedProject(t, q, f.tenant.ID, name+" project", "en", []string{"de"})
	f.file = testutil.SeedFile(t, q, f.project.ID, fixtureFileName, db.FileFormatTxt)
	f.job = testutil.SeedJob(t, q, f.file.ID, "de")
	linguist := f.users["linguist"].ID
	testutil.SeedJobStep(t, q, f.project.SettingsProfileID, f.job.ID, &linguist)
	f.ids = seedItems(t, q, f)
	return f
}

// seedItems creates one item of every kind a v1 path names and returns the
// ids by path parameter.
func seedItems(t *testing.T, q db.Querier, f *fixture) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	tenantID, owner := f.tenant.ID, f.users["owner"].ID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	seg := testutil.SeedSegment(t, q, f.file.ID, "Hello world.", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{TranslationID: tr.ID, UserID: &owner, Content: "check this"})
	must(err)
	base := testutil.SeedTermBase(t, q, tenantID, "Glossary", "en", "de")
	concept, err := q.CreateTermConcept(ctx, db.CreateTermConceptParams{TermBaseID: base.ID, Definition: "a greeting"})
	must(err)
	// The workflow uses its own step, so the step the paths name is free to
	// be replaced or deleted.
	step := testutil.SeedStepDefinition(t, q, tenantID, "Translate", "human", "", nil)
	used := testutil.SeedStepDefinition(t, q, tenantID, "Review", "human", "", nil)
	wf, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{TenantID: tenantID, Name: "Standard"})
	must(err)
	_, err = q.CreateWorkflowDefinitionStep(ctx, db.CreateWorkflowDefinitionStepParams{WorkflowID: wf.ID, StepDefinitionID: used.ID})
	must(err)
	lbl, err := q.CreateLabelDefinition(ctx, db.CreateLabelDefinitionParams{TenantID: tenantID, Name: "Priority"})
	must(err)
	val, err := q.CreateLabelValue(ctx, db.CreateLabelValueParams{LabelDefinitionID: lbl.ID, Value: "High"})
	must(err)
	// A member no key belongs to, so changing or removing it leaves the
	// callers intact.
	target := testutil.SeedUserWithRole(t, q, tenantID, fmt.Sprintf("target-%d@example.com", tenantID), "pass", "linguist")
	inv, err := q.CreateInvite(ctx, db.CreateInviteParams{TenantID: tenantID, Email: fmt.Sprintf("new-%d@example.com", tenantID),
		Role: "linguist", InvitedBy: owner, Token: fmt.Sprintf("invite-token-%d", tenantID),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}})
	must(err)
	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{TenantID: tenantID, Name: "Review prompt", PromptText: "Check the translation.", OutStatus: true, OutNote: true})
	must(err)
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenantID, Name: "Reviewer", Purpose: "qe_ape"})
	must(err)

	// An empty memory has nothing to export, so it holds one entry.
	tm := testutil.SeedTM(t, q, tenantID, "Memory")
	_, err = q.InsertTM(ctx, db.InsertTMParams{TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Good morning.", TargetText: "Guten Morgen.", CreatedBy: "test"})
	must(err)

	return map[string]int64{
		"translationID":    tr.ID,
		"commentID":        comment.ID,
		"tmID":             tm.ID,
		"glossaryID":       base.ID,
		"conceptID":        concept.ID,
		"templateID":       testutil.SeedTemplate(t, q, tenantID, "Template").ID,
		"workflowID":       wf.ID,
		"stepDefinitionID": step.ID,
		"labelID":          lbl.ID,
		"valueID":          val.ID,
		"userID":           target.ID,
		"inviteID":         inv.ID,
		"promptID":         prompt.ID,
		"engineID":         engine.ID,
		// Not a path parameter: the AI connection an engine body names.
		"tokenID": seedCustomProviderToken(t, q, tenantID),
	}
}

// path fills a spec path template with this fixture's ids and prefixes /api/v1.
func (f *fixture) path(template string) string {
	return "/api/v1" + f.fill(template)
}

// fill replaces every path parameter in a route with this fixture's ids. An
// unknown parameter is left in place, so the request 404s and the test that
// sent it fails loudly.
func (f *fixture) fill(template string) string {
	pairs := []string{
		"{projectID}", fmt.Sprint(f.project.ID),
		"{fileID}", fmt.Sprint(f.file.ID),
		"{jobID}", fmt.Sprint(f.job.ID),
	}
	for name, id := range f.ids {
		pairs = append(pairs, "{"+name+"}", fmt.Sprint(id))
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// fixtureFileName is the fixture's file; its stored original is
// fixtureFileContent.
const (
	fixtureFileName    = "brochure.txt"
	fixtureFileContent = "Hello world.\n"
)

// newRouter is the app router with in-memory file storage that holds the
// original of every fixture's file (they share a storage path).
func newRouter(t *testing.T) (db.Querier, chi.Router, func()) {
	t.Helper()
	_, q, router, cleanup := newRouterWithPool(t)
	return q, router, cleanup
}

// newRouterWithPool is newRouter plus the pool, for a test that sets up rows
// no query writes.
func newRouterWithPool(t *testing.T) (*pgxpool.Pool, db.Querier, chi.Router, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	integrationRan.Store(true)
	store := file.NewMemoryStore()
	if err := store.Put(context.Background(), "test/"+fixtureFileName, strings.NewReader(fixtureFileContent)); err != nil {
		t.Fatal(err)
	}
	return pool, q, testutil.NewTestRouterWithStorage(pool, &testutil.RecordingEnqueuer{}, store), cleanup
}

// key mints a new read-and-write key for role, so a long walk of calls does
// not run into the per-key rate limit.
func (f *fixture) key(t *testing.T, role string) string {
	t.Helper()
	return testutil.SeedAPIToken(t, f.q, f.tenant.ID, f.users[role].ID, "write", time.Hour)
}

// seedCustomProviderToken installs the mock custom provider and returns a key
// for it: an engine on it needs no prompt, so engine bodies stay minimal.
func seedCustomProviderToken(t *testing.T, q db.Querier, tenantID int64) int64 {
	t.Helper()
	testutil.SeedExtension(t, q, tenantID, testutil.MockExtensionID)
	return testutil.SeedAIVendorToken(t, q, tenantID, extension.Provider(testutil.MockExtensionID), "Default", "enc").ID
}
