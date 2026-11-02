package terminology_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/terminology"
	"openloc/internal/testutil"
)

// quickAddFixture: an en→de job in a project that uses one term base for the
// pair and another for en→fr only; a manager, the assigned linguist, an
// unassigned linguist; and a second org with its own base.
type quickAddFixture struct {
	pool                   *pgxpool.Pool
	q                      db.Querier
	router                 http.Handler
	tenant                 int64
	pm, linguist, outsider db.User
	project                db.Project
	job                    db.Job
	base, frBase, unused   db.TermBasis
	otherBase              db.TermBasis
	roles                  map[int64]string
}

func newQuickAddFixture(t *testing.T) (*quickAddFixture, func()) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	ctx := context.Background()
	f := &quickAddFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool)}
	f.tenant = testutil.SeedTenant(t, q, "TermCorp").ID
	f.pm = testutil.SeedUserWithRole(t, q, f.tenant, "pm@test.com", "pass", "project_manager")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "ana@test.com", "pass", "linguist")
	f.outsider = testutil.SeedUserWithRole(t, q, f.tenant, "out@test.com", "pass", "linguist")
	f.roles = map[int64]string{f.pm.ID: "project_manager", f.linguist.ID: "linguist", f.outsider.ID: "linguist"}

	f.project = testutil.SeedProject(t, q, f.tenant, "Quick add", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, f.project.ID, "doc.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Reset your password now", 0)
	testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	f.job = testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedJobStep(t, q, f.project.SettingsProfileID, f.job.ID, &f.linguist.ID)

	f.base = testutil.SeedTermBase(t, q, f.tenant, "Main", "en", "de")
	f.frBase = testutil.SeedTermBase(t, q, f.tenant, "French only", "en", "fr")
	f.unused = testutil.SeedTermBase(t, q, f.tenant, "Not on the project", "en", "de")
	for _, a := range []struct {
		base db.TermBasis
		tgt  string
	}{{f.base, "de"}, {f.frBase, "fr"}} {
		if _, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
			SettingsProfileID: f.project.SettingsProfileID, TermBaseID: a.base.ID,
			SourceLanguage: "en", TargetLanguage: a.tgt, Mode: "read_write", Priority: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	other := testutil.SeedTenant(t, q, "OtherCorp").ID
	f.otherBase = testutil.SeedTermBase(t, q, other, "Theirs", "en", "de")
	return f, cleanup
}

func (f *quickAddFixture) do(t *testing.T, u db.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, method, path, body, u.ID, f.tenant, f.roles[u.ID])
}

func (f *quickAddFixture) add(t *testing.T, u db.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, u, http.MethodPost, fmt.Sprintf("/api/jobs/%d/terms", f.job.ID), body)
}

type quickAddResp struct {
	Data terminology.QuickAddResult `json:"data"`
}

// lookup is what the editor's terminology panel asks for the segment.
func (f *quickAddFixture) lookup(t *testing.T, u db.User, sourceText string) []struct {
	SourceTerm string `json:"source_term"`
	TargetTerm string `json:"target_term"`
	Status     string `json:"status"`
} {
	t.Helper()
	path := fmt.Sprintf("/api/terminology/lookup?project_id=%d&source_language=en&target_language=de&source_text=%s",
		f.project.ID, url.QueryEscape(sourceText))
	rec := f.do(t, u, http.MethodGet, path, nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []struct {
			SourceTerm string `json:"source_term"`
			TargetTerm string `json:"target_term"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}

func TestIntegrationJobTermBasesListsThePairsBases(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()

	rec := f.do(t, f.linguist, http.MethodGet, fmt.Sprintf("/api/jobs/%d/term-bases", f.job.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data terminology.JobTermBases `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if b := resp.Data.TermBases; len(b) != 1 || b[0].ID != f.base.ID || b[0].Name != "Main" || !resp.Data.CanAdd {
		t.Fatalf("term bases = %+v, want only %q (the en→fr and unassigned bases left out) and can_add", resp.Data, "Main")
	}

	rec = f.do(t, f.outsider, http.MethodGet, fmt.Sprintf("/api/jobs/%d/term-bases", f.job.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}

func TestIntegrationQuickAddLinguistTermIsPendingAndMatches(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()

	rec := f.add(t, f.linguist, map[string]any{
		"term_base_id": f.base.ID, "source_term": " password ", "target_term": "Passwort",
		"status": "approved", "definition": "a secret word", "notes": "from the editor",
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var got quickAddResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Status != "pending" || !got.Data.CreatedConcept || got.Data.SourceTerm != "password" {
		t.Fatalf("result = %+v, want a new concept with a pending term for %q", got.Data, "password")
	}

	matches := f.lookup(t, f.linguist, "Reset your password now")
	if len(matches) != 1 || matches[0].SourceTerm != "password" || matches[0].TargetTerm != "Passwort" || matches[0].Status != "pending" {
		t.Fatalf("lookup = %+v, want the new pending password→Passwort", matches)
	}
	var def, lang string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT c.definition, t.language FROM term_concept c JOIN term t ON t.concept_id = c.id
		 WHERE c.id = $1 AND t.id = $2`, got.Data.ConceptID, got.Data.TermID).Scan(&def, &lang); err != nil {
		t.Fatal(err)
	}
	if def != "a secret word" || lang != "de" {
		t.Errorf("definition %q, target language %q; want %q, %q", def, lang, "a secret word", "de")
	}
}

func TestIntegrationQuickAddManagerKeepsStatus(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()

	rec := f.add(t, f.pm, map[string]any{
		"term_base_id": f.base.ID, "source_term": "password", "target_term": "Passwort", "status": "forbidden",
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var got quickAddResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Status != "forbidden" {
		t.Fatalf("status = %q, want the manager's %q", got.Data.Status, "forbidden")
	}
	rec = f.add(t, f.pm, map[string]any{
		"term_base_id": f.base.ID, "source_term": "password", "target_term": "Kennwort", "status": "bogus",
	})
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationQuickAddRefusals(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()
	body := func(baseID int64) map[string]any {
		return map[string]any{"term_base_id": baseID, "source_term": "password", "target_term": "Passwort"}
	}
	// Another org's base, assigned to this project's profile, so only the
	// tenant checks can refuse it.
	if _, err := f.q.CreateSettingsTermAssignment(context.Background(), db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: f.project.SettingsProfileID, TermBaseID: f.otherBase.ID,
		SourceLanguage: "en", TargetLanguage: "de", Mode: "read_write", Priority: 2,
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		user db.User
		body map[string]any
		want int
	}{
		{"unassigned linguist", f.outsider, body(f.base.ID), http.StatusForbidden},
		{"base not on the project", f.pm, body(f.unused.ID), http.StatusNotFound},
		{"base on the project for another pair", f.pm, body(f.frBase.ID), http.StatusNotFound},
		{"another org's base", f.pm, body(f.otherBase.ID), http.StatusNotFound},
		{"empty target", f.linguist, map[string]any{"term_base_id": f.base.ID, "source_term": "password", "target_term": " "}, http.StatusBadRequest},
		{"no base", f.linguist, map[string]any{"source_term": "password", "target_term": "Passwort"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.AssertStatus(t, f.add(t, c.user, c.body), c.want)
		})
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM term WHERE text IN ('password','Passwort')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d terms written by refused requests, want 0", n)
	}

	// A job in another org is not found, whatever base is named.
	otherTenant := testutil.SeedTenant(t, f.q, "ThirdCorp").ID
	stranger := testutil.SeedUserWithRole(t, f.q, otherTenant, "pm@third.test", "pass", "project_manager")
	rec := testutil.DoRequestWithRole(t, f.router, http.MethodPost, fmt.Sprintf("/api/jobs/%d/terms", f.job.ID),
		body(f.base.ID), stranger.ID, otherTenant, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationQuickAddUsesProjectDomain(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()
	testutil.SeedProjectSystemLabel(t, f.q, f.tenant, f.project.ID, "domain", "IT")

	rec := f.add(t, f.pm, map[string]any{"term_base_id": f.base.ID, "source_term": "password", "target_term": "Passwort"})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var got quickAddResp
	testutil.DecodeJSON(t, rec, &got)
	var domain string
	if err := f.pool.QueryRow(context.Background(), `SELECT domain FROM term_concept WHERE id = $1`, got.Data.ConceptID).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if domain != "IT" {
		t.Errorf("concept domain = %q, want the project's %q", domain, "IT")
	}
}

func TestIntegrationQuickAddTermMergesIntoConcept(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "MergeCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "u@test.com", "pass")
	base := testutil.SeedTermBase(t, q, tenant.ID, "Main", "en", "de")
	svc := terminology.NewService(q, pool)

	first, err := svc.QuickAddTerm(ctx, base, terminology.QuickAddInput{
		SourceTerm: "password", TargetTerm: "Passwort", TargetLanguage: "de", Definition: "first",
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "approved" || !first.CreatedConcept {
		t.Fatalf("first = %+v, want a new concept with an approved term", first)
	}

	again, err := svc.QuickAddTerm(ctx, base, terminology.QuickAddInput{
		SourceTerm: "password", TargetTerm: "Passwort", TargetLanguage: "de", Status: "pending",
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.TermID != first.TermID || again.CreatedConcept || again.Status != "approved" {
		t.Errorf("same pair again = %+v, want the existing term %d unchanged", again, first.TermID)
	}

	second, err := svc.QuickAddTerm(ctx, base, terminology.QuickAddInput{
		SourceTerm: "password", TargetTerm: "Kennwort", TargetLanguage: "de", Definition: "second",
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ConceptID != first.ConceptID || second.CreatedConcept || second.TermID == first.TermID {
		t.Errorf("second target = %+v, want a new term in concept %d", second, first.ConceptID)
	}

	var concepts int
	var def string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(definition) FROM term_concept WHERE term_base_id = $1 AND deleted_at IS NULL`,
		base.ID).Scan(&concepts, &def); err != nil {
		t.Fatal(err)
	}
	if concepts != 1 || def != "first" {
		t.Errorf("%d concepts with definition %q, want 1 with %q (a definition never overwrites)", concepts, def, "first")
	}
	var langs []string
	if err := pool.QueryRow(ctx, `SELECT target_languages FROM term_bases WHERE id = $1`, base.ID).Scan(&langs); err != nil {
		t.Fatal(err)
	}
	if len(langs) != 1 || langs[0] != "de" {
		t.Errorf("target languages = %v, want [de]", langs)
	}
}

// With "Linguists can add terms" off, a linguist is told so and refused;
// a manager still adds.
func TestIntegrationQuickAddSwitchedOffForLinguists(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()
	rec := f.do(t, f.pm, http.MethodPatch, fmt.Sprintf("/api/projects/%d/settings", f.project.ID),
		map[string]any{"linguist_terms": "off"})
	testutil.AssertStatus(t, rec, http.StatusOK)

	canAdd := func(u db.User) bool {
		t.Helper()
		rec := f.do(t, u, http.MethodGet, fmt.Sprintf("/api/jobs/%d/term-bases", f.job.ID), nil)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data terminology.JobTermBases `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data.CanAdd
	}
	if canAdd(f.linguist) {
		t.Error("can_add = true for a linguist with the setting off")
	}
	if !canAdd(f.pm) {
		t.Error("can_add = false for a manager, want true whatever the setting")
	}

	body := map[string]any{"term_base_id": f.base.ID, "source_term": "password", "target_term": "Passwort"}
	testutil.AssertStatus(t, f.add(t, f.linguist, body), http.StatusForbidden)
	if n := testutil.CountRows(t, f.pool, `SELECT count(*) FROM term WHERE text = 'Passwort'`); n != 0 {
		t.Fatalf("%d terms written by a refused linguist, want 0", n)
	}
	testutil.AssertStatus(t, f.add(t, f.pm, body), http.StatusCreated)
}

// With "Linguists can add terms" set to approved, a linguist's term is saved
// approved, whatever status they send, and the list tells the dialog so.
func TestIntegrationQuickAddLinguistTermsApproved(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()
	rec := f.do(t, f.pm, http.MethodPatch, fmt.Sprintf("/api/projects/%d/settings", f.project.ID),
		map[string]any{"linguist_terms": "approved"})
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = f.do(t, f.linguist, http.MethodGet, fmt.Sprintf("/api/jobs/%d/term-bases", f.job.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data terminology.JobTermBases `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if !list.Data.CanAdd || list.Data.LinguistStatus != "approved" {
		t.Errorf("term-bases = %+v, want can_add and linguist_status approved", list.Data)
	}

	rec = f.add(t, f.linguist, map[string]any{
		"term_base_id": f.base.ID, "source_term": "password", "target_term": "Passwort", "status": "forbidden",
	})
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var got quickAddResp
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Status != "approved" {
		t.Errorf("status = %q, want the project's approved, not the linguist's forbidden", got.Data.Status)
	}
}

// Quick-add writes only to term bases whose assignment allows Write: a
// Read-only one is not offered, and adding to it is refused.
func TestIntegrationQuickAddOnlyWritableBases(t *testing.T) {
	f, cleanup := newQuickAddFixture(t)
	defer cleanup()
	readOnly := testutil.SeedTermBase(t, f.q, f.tenant, "Read only", "en", "de")
	if _, err := f.q.CreateSettingsTermAssignment(context.Background(), db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: f.project.SettingsProfileID, TermBaseID: readOnly.ID,
		SourceLanguage: "en", TargetLanguage: "de", Mode: "read", Priority: 0,
	}); err != nil {
		t.Fatal(err)
	}

	rec := f.do(t, f.pm, http.MethodGet, fmt.Sprintf("/api/jobs/%d/term-bases", f.job.ID), nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data terminology.JobTermBases `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	for _, b := range list.Data.TermBases {
		if b.ID == readOnly.ID {
			t.Errorf("term-bases offers the Read-only base %q", b.Name)
		}
	}
	testutil.AssertStatus(t, f.add(t, f.pm, map[string]any{
		"term_base_id": readOnly.ID, "source_term": "password", "target_term": "Passwort",
	}), http.StatusNotFound)
}
