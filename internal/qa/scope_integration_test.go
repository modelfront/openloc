package qa_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
	"openloc/internal/testutil"
)

// scopeQAFixture: a German project with files A and B, two rows each, one of
// them with a dropped number; a QA profile with the number check once
// withProfile runs. The linguist is assigned A only.
type scopeQAFixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	router   http.Handler
	owner    int64
	tenant   int64
	linguist db.User
	project  db.Project
	jobs     map[string]db.Job
	tr       map[string]int64 // "A:bad" → translation id
}

func newScopeQAFixture(t *testing.T) *scopeQAFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	f := &scopeQAFixture{pool: pool, q: q, router: testutil.NewTestRouter(pool), jobs: map[string]db.Job{}, tr: map[string]int64{}}
	f.owner, f.tenant = testutil.RegisterUser(t, q, "qa-scope")
	f.linguist = testutil.SeedUserWithRole(t, q, f.tenant, "qa-scope-ling@test.com", "pw", "linguist")
	f.project = testutil.SeedProject(t, q, f.tenant, "QA scope", "en", []string{"de"})
	for _, name := range []string{"A", "B"} {
		file := testutil.SeedFile(t, q, f.project.ID, name, db.FileFormatJsonI18n)
		f.jobs[name] = testutil.SeedJob(t, q, file.ID, "de")
		for i, row := range [][2]string{{"There are 42 items", "Es gibt Dinge"}, {"Nothing numeric", "Nichts"}} {
			seg := testutil.SeedSegment(t, q, file.ID, row[0], int32(i))
			tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
			f.exec(t, `UPDATE translations SET active_text = $2 WHERE id = $1`, tr.ID, row[1])
			f.tr[name+":"+[]string{"bad", "good"}[i]] = tr.ID
		}
		var assignee *int64
		if name == "A" {
			assignee = &f.linguist.ID
		}
		testutil.SeedJobStep(t, q, f.project.SettingsProfileID, f.jobs[name].ID, assignee)
	}
	return f
}

func (f *scopeQAFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(testutil.Ctx(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *scopeQAFixture) withProfile(t *testing.T) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles", map[string]string{"name": "Scope QA"}, f.owner, f.tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	profile := testutil.DecodeData[struct {
		ID int64 `json:"id"`
	}](t, rec)
	testutil.AssertStatus(t, testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", profile.ID),
		map[string]any{"check_type": "number_consistency", "enabled": true, "severity": "error"}, f.owner, f.tenant), http.StatusOK)
	testutil.AssertStatus(t, testutil.DoRequest(t, f.router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", f.project.ID),
		map[string]any{"qa_profile_id": profile.ID}, f.owner, f.tenant), http.StatusOK)
}

func (f *scopeQAFixture) path(route string, jobs ...db.Job) string {
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = fmt.Sprint(j.ID)
	}
	return fmt.Sprintf("/api/projects/%d/editor/%s?jobs=%s", f.project.ID, route, strings.Join(ids, ","))
}

func (f *scopeQAFixture) do(t *testing.T, user int64, role, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, method, path, body, user, f.tenant, role)
}

func (f *scopeQAFixture) run(t *testing.T, body any, jobs ...db.Job) []qa.ScopeRunJob {
	t.Helper()
	rec := f.do(t, f.owner, "owner", http.MethodPost, f.path("run-qa", jobs...), body)
	testutil.AssertStatus(t, rec, http.StatusOK)
	return testutil.DecodeData[[]qa.ScopeRunJob](t, rec)
}

func (f *scopeQAFixture) report(t *testing.T, path string) qa.ReportDTO {
	t.Helper()
	rec := f.do(t, f.owner, "owner", http.MethodGet, path, nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	return testutil.DecodeData[qa.ReportDTO](t, rec)
}

func (f *scopeQAFixture) findings(t *testing.T, id int64) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT count(*) FROM qa_results WHERE translation_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A run over the scope checks every file and answers per job; the scope's
// report is the files' reports put together, in scope order.
func TestIntegrationQAScope_RunAndReportCoverEveryFile(t *testing.T) {
	f := newScopeQAFixture(t)
	f.withProfile(t)
	res := f.run(t, nil, f.jobs["B"], f.jobs["A"])
	want := []qa.ScopeRunJob{
		{JobID: f.jobs["B"].ID, Outcome: qa.ScopeRunDone, Issues: 1},
		{JobID: f.jobs["A"].ID, Outcome: qa.ScopeRunDone, Issues: 1},
	}
	if !slices.Equal(res, want) {
		t.Fatalf("run = %+v, want %+v", res, want)
	}
	// One of A's findings ignored: it counts as ignored in both reports.
	f.exec(t, `UPDATE qa_results SET ignored = true WHERE translation_id = $1`, f.tr["A:bad"])

	scope := f.report(t, f.path("qa-report", f.jobs["B"], f.jobs["A"]))
	a := f.report(t, fmt.Sprintf("/api/jobs/%d/qa-report", f.jobs["A"].ID))
	b := f.report(t, fmt.Sprintf("/api/jobs/%d/qa-report", f.jobs["B"].ID))
	if !slices.Equal(scope.Results, append(b.Results, a.Results...)) {
		t.Errorf("scope results = %+v, want B's then A's: %+v %+v", scope.Results, b.Results, a.Results)
	}
	wantSummary := qa.ReportSummary{Error: b.Summary.Error + a.Summary.Error, Ignored: a.Summary.Ignored}
	if scope.Summary != wantSummary || wantSummary != (qa.ReportSummary{Error: 1, Ignored: 1}) {
		t.Errorf("scope summary = %+v, want %+v (one open error in B, one ignored in A)", scope.Summary, wantSummary)
	}
	if !scope.ProfileAssigned {
		t.Error("profile_assigned = false, want true")
	}
}

// A run on a selection checks the selected rows of each file and nothing else.
func TestIntegrationQAScope_SelectionRunTouchesOnlyTheSelectedRows(t *testing.T) {
	f := newScopeQAFixture(t)
	f.withProfile(t)
	res := f.run(t, map[string]any{"translation_ids": []int64{f.tr["A:bad"], f.tr["B:good"]}}, f.jobs["A"], f.jobs["B"])
	if res[0].Issues != 1 || res[1].Issues != 0 || res[0].Outcome != qa.ScopeRunDone || res[1].Outcome != qa.ScopeRunDone {
		t.Errorf("run = %+v, want one finding in A, none in B", res)
	}
	for key, want := range map[string]int{"A:bad": 1, "A:good": 0, "B:bad": 0, "B:good": 0} {
		if got := f.findings(t, f.tr[key]); got != want {
			t.Errorf("%s: %d findings, want %d", key, got, want)
		}
	}
	rec := f.do(t, f.owner, "owner", http.MethodPost, f.path("run-qa", f.jobs["A"]), map[string]any{"translation_ids": []int64{}})
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

// Without a QA profile, each job says so and nothing runs; the report says
// no profile is assigned.
func TestIntegrationQAScope_NoProfileIsReportedPerJob(t *testing.T) {
	f := newScopeQAFixture(t)
	res := f.run(t, nil, f.jobs["A"], f.jobs["B"])
	for _, r := range res {
		if r.Outcome != qa.ScopeRunNoProfile {
			t.Errorf("job %d outcome = %q, want %q", r.JobID, r.Outcome, qa.ScopeRunNoProfile)
		}
	}
	if rep := f.report(t, f.path("qa-report", f.jobs["A"], f.jobs["B"])); rep.ProfileAssigned {
		t.Error("profile_assigned = true, want false")
	}
}

// The scope routes refuse what the editor scope refuses: another tenant's or
// another project's job, and a linguist's job not assigned to them.
func TestIntegrationQAScope_RefusesWhatTheScopeRefuses(t *testing.T) {
	f := newScopeQAFixture(t)
	f.withProfile(t)
	otherUser, otherTenant := testutil.RegisterUser(t, f.q, "qa-scope-other")
	otherProject := testutil.SeedProject(t, f.q, otherTenant, "Other", "en", []string{"de"})
	otherJob := testutil.SeedJob(t, f.q, testutil.SeedFile(t, f.q, otherProject.ID, "X", db.FileFormatJsonI18n).ID, "de")
	secondProject := testutil.SeedProject(t, f.q, f.tenant, "Second", "en", []string{"de"})
	secondJob := testutil.SeedJob(t, f.q, testutil.SeedFile(t, f.q, secondProject.ID, "Y", db.FileFormatJsonI18n).ID, "de")

	for _, route := range []string{"qa-report", "run-qa"} {
		method := http.MethodGet
		if route == "run-qa" {
			method = http.MethodPost
		}
		for _, tc := range []struct {
			name   string
			user   int64
			tenant int64
			role   string
			path   string
			want   int
		}{
			{"another tenant's job", f.owner, f.tenant, "owner", f.path(route, f.jobs["A"], otherJob), http.StatusNotFound},
			{"a job of another project", f.owner, f.tenant, "owner", f.path(route, f.jobs["A"], secondJob), http.StatusNotFound},
			{"another tenant asking", otherUser, otherTenant, "owner", f.path(route, f.jobs["A"]), http.StatusNotFound},
			{"a linguist on a job not theirs", f.linguist.ID, f.tenant, "linguist", f.path(route, f.jobs["A"], f.jobs["B"]), http.StatusForbidden},
			{"a linguist's own job", f.linguist.ID, f.tenant, "linguist", f.path(route, f.jobs["A"]), http.StatusOK},
		} {
			t.Run(route+": "+tc.name, func(t *testing.T) {
				rec := testutil.DoRequestWithRole(t, f.router, method, tc.path, nil, tc.user, tc.tenant, tc.role)
				testutil.AssertStatus(t, rec, tc.want)
			})
		}
	}
	// The refused runs wrote nothing on B.
	if n := f.findings(t, f.tr["B:bad"]); n != 0 {
		t.Errorf("B:bad has %d findings after refused runs, want 0", n)
	}
}

// job_ids narrows a run to those files of the scope, so the editor can send a
// large scope in parts; a job outside the scope is refused.
func TestIntegrationQAScope_JobIDsNarrowTheRun(t *testing.T) {
	f := newScopeQAFixture(t)
	f.withProfile(t)
	res := f.run(t, map[string]any{"job_ids": []int64{f.jobs["B"].ID}}, f.jobs["A"], f.jobs["B"])
	if want := []qa.ScopeRunJob{{JobID: f.jobs["B"].ID, Outcome: qa.ScopeRunDone, Issues: 1}}; !slices.Equal(res, want) {
		t.Errorf("run = %+v, want only B: %+v", res, want)
	}
	if n := f.findings(t, f.tr["A:bad"]); n != 0 {
		t.Errorf("A:bad has %d findings, want 0: A was not asked for", n)
	}
	rec := f.do(t, f.owner, "owner", http.MethodPost, f.path("run-qa", f.jobs["A"]), map[string]any{"job_ids": []int64{f.jobs["B"].ID}})
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}
