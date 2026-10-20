package job_test

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type taskPageResp struct {
	Data []struct {
		JobID        int64   `json:"job_id"`
		StepName     string  `json:"step_name"`
		Deadline     *string `json:"deadline"`
		AssigneeName *string `json:"assignee_name"`
	} `json:"data"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

func (p taskPageResp) jobIDs() []int64 {
	ids := make([]int64, 0, len(p.Data))
	for _, row := range p.Data {
		ids = append(ids, row.JobID)
	}
	return ids
}

// seedTask creates a job on file with its current step assigned to assignee,
// then sets the step's name and the assignment's deadline (nil = none).
func seedTask(t *testing.T, pool *pgxpool.Pool, q db.Querier, profileID, fileID int64, lang string, assignee int64, step string, deadline *time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	job := testutil.SeedJob(t, q, fileID, lang)
	jsaID := testutil.SeedJobStep(t, q, profileID, job.ID, &assignee)
	if _, err := pool.Exec(ctx, `UPDATE workflow_steps SET name = $1
		WHERE id = (SELECT workflow_step_id FROM job_step_assignments WHERE id = $2)`, step, jsaID); err != nil {
		t.Fatalf("set step name: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE job_step_assignments SET deadline = $1 WHERE id = $2`, deadline, jsaID); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	return job.ID
}

func getTasks(t *testing.T, router http.Handler, userID, tenantID int64, role string, params url.Values) taskPageResp {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tasks?"+params.Encode(), nil, userID, tenantID, role)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page taskPageResp
	testutil.DecodeJSON(t, rec, &page)
	return page
}

func getTaskSteps(t *testing.T, router http.Handler, userID, tenantID int64, role string) []string {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tasks/steps", nil, userID, tenantID, role)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data []string `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data
}

// TestTasksFilters covers every Tasks page filter against the real query,
// alone and combined, for both scopes (owner/admin see everyone, project
// managers and linguists only their own) — and that no filter widens the
// narrow scope.
func TestTasksFilters(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "FilterCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	ling := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")
	other := testutil.SeedUserWithRole(t, q, tenant.ID, "other@test.com", "pass", "linguist")

	project := testutil.SeedProject(t, q, tenant.ID, "Brochure", "en", []string{"de", "fr", "it", "es", "pt", "nl"})
	manual := testutil.SeedFile(t, q, project.ID, "manual.docx", db.FileFormatJsonI18n)
	slides := testutil.SeedFile(t, q, project.ID, "slides.pptx", db.FileFormatJsonI18n)
	pid := project.SettingsProfileID

	now := time.Now()
	at := func(d time.Duration) *time.Time { return ptr.To(now.Add(d)) }

	overdue := seedTask(t, pool, q, pid, manual.ID, "de", ling.ID, "Translation", at(-2*time.Hour))
	in3h := seedTask(t, pool, q, pid, manual.ID, "fr", ling.ID, "Review", at(3*time.Hour))
	in3d := seedTask(t, pool, q, pid, slides.ID, "it", ling.ID, "Translation", at(72*time.Hour))
	in30d := seedTask(t, pool, q, pid, slides.ID, "es", ling.ID, "Review", at(30*24*time.Hour))
	noDeadline := seedTask(t, pool, q, pid, slides.ID, "pt", ling.ID, "Translation", nil)
	// Someone else's task: owner sees it, ling must never see it.
	othersTask := seedTask(t, pool, q, pid, manual.ID, "nl", other.ID, "Translation", at(-time.Hour))

	cases := []struct {
		name   string
		params url.Values
		ling   []int64 // expected for the linguist, in order
		owner  []int64 // expected for the owner, in order
	}{
		{"no filter", url.Values{},
			[]int64{overdue, in3h, in3d, in30d, noDeadline},
			[]int64{overdue, othersTask, in3h, in3d, in30d, noDeadline}},
		{"overdue", url.Values{"deadline": {"overdue"}},
			[]int64{overdue}, []int64{overdue, othersTask}},
		// 24h includes in3h only; the overdue task is not "due within a day".
		{"24h", url.Values{"deadline": {"24h"}}, []int64{in3h}, []int64{in3h}},
		{"week", url.Values{"deadline": {"week"}}, []int64{in3h, in3d}, []int64{in3h, in3d}},
		{"none", url.Values{"deadline": {"none"}}, []int64{noDeadline}, []int64{noDeadline}},
		{"step", url.Values{"step": {"Review"}}, []int64{in3h, in30d}, []int64{in3h, in30d}},
		{"q matches file name", url.Values{"q": {"SLIDES"}},
			[]int64{in3d, in30d, noDeadline}, []int64{in3d, in30d, noDeadline}},
		{"q matches project name", url.Values{"q": {"broch"}},
			[]int64{overdue, in3h, in3d, in30d, noDeadline},
			[]int64{overdue, othersTask, in3h, in3d, in30d, noDeadline}},
		{"q no match", url.Values{"q": {"zzz"}}, []int64{}, []int64{}},
		{"combined", url.Values{"q": {"manual"}, "step": {"Translation"}, "deadline": {"overdue"}},
			[]int64{overdue}, []int64{overdue, othersTask}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getTasks(t, router, ling.ID, tenant.ID, "linguist", tc.params).jobIDs(); !slices.Equal(got, tc.ling) {
				t.Errorf("linguist: got %v, want %v", got, tc.ling)
			}
			if got := getTasks(t, router, owner.ID, tenant.ID, "owner", tc.params).jobIDs(); !slices.Equal(got, tc.owner) {
				t.Errorf("owner: got %v, want %v", got, tc.owner)
			}
		})
	}

	t.Run("assignee name only for owner/admin", func(t *testing.T) {
		for _, row := range getTasks(t, router, owner.ID, tenant.ID, "owner", url.Values{}).Data {
			want := "ling@test.com"
			if row.JobID == othersTask {
				want = "other@test.com"
			}
			if row.AssigneeName == nil || *row.AssigneeName != want {
				t.Errorf("owner view job %d: assignee_name = %v, want %q", row.JobID, row.AssigneeName, want)
			}
		}
		for _, row := range getTasks(t, router, ling.ID, tenant.ID, "linguist", url.Values{}).Data {
			if row.AssigneeName != nil {
				t.Errorf("linguist view job %d: assignee_name = %q, want omitted", row.JobID, *row.AssigneeName)
			}
		}
	})

	t.Run("bad params are rejected", func(t *testing.T) {
		for _, qs := range []string{"deadline=tomorrow", "cursor=garbage", "cursor=inf:0"} {
			rec := testutil.DoRequestWithRole(t, router, "GET", "/api/tasks?"+qs, nil, owner.ID, tenant.ID, "owner")
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
		}
	})

	t.Run("step names follow the caller's scope", func(t *testing.T) {
		// Only the other linguist's task carries this step name.
		if _, err := pool.Exec(context.Background(), `UPDATE workflow_steps SET name = 'Proofread'
			WHERE id = (SELECT jsa.workflow_step_id FROM jobs j
			            JOIN job_step_assignments jsa ON jsa.id = j.current_step_id WHERE j.id = $1)`, othersTask); err != nil {
			t.Fatalf("rename step: %v", err)
		}
		if got, want := getTaskSteps(t, router, ling.ID, tenant.ID, "linguist"), []string{"Review", "Translation"}; !slices.Equal(got, want) {
			t.Errorf("linguist steps = %v, want %v", got, want)
		}
		if got, want := getTaskSteps(t, router, owner.ID, tenant.ID, "owner"), []string{"Proofread", "Review", "Translation"}; !slices.Equal(got, want) {
			t.Errorf("owner steps = %v, want %v", got, want)
		}
		// A step filter for someone else's step name returns nothing to the linguist.
		if got := getTasks(t, router, ling.ID, tenant.ID, "linguist", url.Values{"step": {"Proofread"}}).jobIDs(); len(got) != 0 {
			t.Errorf("linguist step=Proofread: got %v, want none", got)
		}
	})

	// The scope line sits between admin and project manager, so pin both sides
	// of it: a manager is narrow like a linguist, an admin wide like an owner.
	t.Run("project manager sees own tasks, admin sees everyone's", func(t *testing.T) {
		pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
		admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
		pmTask := seedTask(t, pool, q, pid, slides.ID, "de", pm.ID, "PM review", nil)

		pmPage := getTasks(t, router, pm.ID, tenant.ID, "project_manager", url.Values{})
		if got, want := pmPage.jobIDs(), []int64{pmTask}; !slices.Equal(got, want) {
			t.Errorf("project manager: got %v, want %v", got, want)
		}
		for _, row := range pmPage.Data {
			if row.AssigneeName != nil {
				t.Errorf("project manager view job %d: assignee_name = %q, want omitted", row.JobID, *row.AssigneeName)
			}
		}
		if got, want := getTaskSteps(t, router, pm.ID, tenant.ID, "project_manager"), []string{"PM review"}; !slices.Equal(got, want) {
			t.Errorf("project manager steps = %v, want %v", got, want)
		}

		adminPage := getTasks(t, router, admin.ID, tenant.ID, "admin", url.Values{})
		// Both no-deadline tasks sort last, newest job first.
		wantAll := []int64{overdue, othersTask, in3h, in3d, in30d, pmTask, noDeadline}
		if got := adminPage.jobIDs(); !slices.Equal(got, wantAll) {
			t.Errorf("admin: got %v, want %v", got, wantAll)
		}
		for _, row := range adminPage.Data {
			if row.AssigneeName == nil || *row.AssigneeName == "" {
				t.Errorf("admin view job %d: assignee_name missing", row.JobID)
			}
		}
	})
}

// TestTasksPagination checks the sort order (deadline, then newest job, then
// no deadline) and walks the list page by page across equal and missing
// deadlines: every task comes back exactly once, in that order.
func TestTasksPagination(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "PageCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	langs := []string{"de", "fr", "it", "es", "pt", "nl", "ja"}
	project := testutil.SeedProject(t, q, tenant.ID, "Paged", "en", langs)
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatJsonI18n)

	shared := time.Now().Add(48 * time.Hour).Truncate(time.Microsecond)
	later := shared.Add(time.Hour)
	var all []int64
	for i, d := range []*time.Time{&shared, nil, &later, &shared, nil, &shared, &later} {
		all = append(all, seedTask(t, pool, q, project.SettingsProfileID, file.ID, langs[i], owner.ID, "Translation", d))
	}

	full := getTasks(t, router, owner.ID, tenant.ID, "owner", url.Values{"limit": {"100"}})
	if full.HasMore || full.Cursor != "" {
		t.Fatalf("one big page: has_more=%v cursor=%q, want false/empty", full.HasMore, full.Cursor)
	}
	// Built from the seeds, not from the query under test: earliest deadline
	// first, newest job first among equal deadlines, no deadline last. Job ids
	// grow in seed order.
	want := []int64{all[5], all[3], all[0], all[6], all[2], all[4], all[1]}
	if got := full.jobIDs(); !slices.Equal(got, want) {
		t.Fatalf("unpaged order = %v, want %v", got, want)
	}

	var got []int64
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(all) {
			t.Fatal("pagination did not terminate")
		}
		params := url.Values{"limit": {"2"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		page := getTasks(t, router, owner.ID, tenant.ID, "owner", params)
		got = append(got, page.jobIDs()...)
		if !page.HasMore {
			break
		}
		cursor = page.Cursor
	}
	if !slices.Equal(got, want) {
		t.Errorf("paged order = %v, want %v", got, want)
	}
}

// TestTasksTenantIsolation: another tenant's tasks and step names never reach
// an owner, with or without filters.
func TestTasksTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	mine := testutil.SeedTenant(t, q, "MineCorp")
	owner := testutil.SeedUser(t, q, mine.ID, "owner@test.com", "pass")
	theirs := testutil.SeedTenant(t, q, "TheirsCorp")
	stranger := testutil.SeedUser(t, q, theirs.ID, "stranger@test.com", "pass")

	p := testutil.SeedProject(t, q, theirs.ID, "Secret", "en", []string{"de"})
	f := testutil.SeedFile(t, q, p.ID, "secret.txt", db.FileFormatJsonI18n)
	seedTask(t, pool, q, p.SettingsProfileID, f.ID, "de", stranger.ID, "Hidden step", nil)

	for _, params := range []url.Values{{}, {"q": {"secret"}}, {"step": {"Hidden step"}}, {"deadline": {"none"}}} {
		if got := getTasks(t, router, owner.ID, mine.ID, "owner", params).jobIDs(); len(got) != 0 {
			t.Errorf("params %v: owner sees another tenant's tasks %v", params, got)
		}
	}
	if steps := getTaskSteps(t, router, owner.ID, mine.ID, "owner"); len(steps) != 0 {
		t.Errorf("owner sees another tenant's step names %v", steps)
	}
}
