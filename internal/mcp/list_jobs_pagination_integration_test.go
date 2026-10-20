package mcp_test

import (
	"context"
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// stepDeadlines gives jobs an active workflow step with a deadline, through
// one Translate step per project.
type stepDeadlines struct {
	t     *testing.T
	f     *fixture
	steps map[int64]int64 // project id → workflow step id
}

func (s *stepDeadlines) set(projectID, jobID int64, deadline time.Time) {
	s.t.Helper()
	ctx := context.Background()
	stepID, ok := s.steps[projectID]
	if !ok {
		project, err := s.f.q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: s.f.tenantA.ID})
		if err != nil {
			s.t.Fatalf("load project: %v", err)
		}
		step, err := s.f.q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: project.SettingsProfileID, Name: "Translate", StepOrder: 1, RoleRequired: "linguist",
		})
		if err != nil {
			s.t.Fatalf("seed step: %v", err)
		}
		stepID = step.ID
		s.steps[projectID] = stepID
	}
	testutil.AssignStep(s.t, s.f.q, jobID, stepID, testutil.WorkflowStepSpec{
		Assignee: &s.f.linguistA.ID, Status: "in_progress", Deadline: deadline,
	})
}

type jobPage struct {
	Jobs []struct {
		ID       int64  `json:"id"`
		Deadline string `json:"deadline"`
	} `json:"jobs"`
	Count      int    `json:"count"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// pagingFixture is tenant A with six jobs: one overdue, two sharing a
// deadline, one later, and two with no deadline (f.jobA and noDeadline).
type pagingFixture struct {
	f                                           *fixture
	overdue, tieLow, tieHigh, later, noDeadline db.Job
	tie                                         time.Time
}

func newPagingFixture(t *testing.T) pagingFixture {
	t.Helper()
	f := newFixture(t)
	projectA2 := testutil.SeedProject(t, f.q, f.tenantA.ID, "Second Alpha", "en", []string{"fr"})
	p := pagingFixture{f: f}
	p.overdue = seedJobWithSegments(t, f.q, projectA2.ID, "overdue.txt", "fr", []string{"One"})
	p.tieLow = seedJobWithSegments(t, f.q, f.projectA.ID, "tie-low.txt", "de", []string{"One"})
	p.tieHigh = seedJobWithSegments(t, f.q, projectA2.ID, "tie-high.txt", "fr", []string{"One"})
	p.later = seedJobWithSegments(t, f.q, f.projectA.ID, "later.txt", "de", []string{"One"})
	p.noDeadline = seedJobWithSegments(t, f.q, projectA2.ID, "none.txt", "fr", []string{"One"})

	now := time.Now().UTC().Truncate(time.Second)
	p.tie = now.Add(48 * time.Hour)
	steps := &stepDeadlines{t: t, f: f, steps: map[int64]int64{}}
	steps.set(projectA2.ID, p.overdue.ID, now.Add(-24*time.Hour))
	steps.set(f.projectA.ID, p.tieLow.ID, p.tie)
	steps.set(projectA2.ID, p.tieHigh.ID, p.tie)
	steps.set(f.projectA.ID, p.later.ID, now.Add(96*time.Hour))
	return p
}

// page calls list_jobs once and fails the test on a tool error.
func (p pagingFixture) page(t *testing.T, s *sdk.ClientSession, args map[string]any) jobPage {
	t.Helper()
	var out jobPage
	if isErr, text := callTool(t, s, "openloc_list_jobs", args, &out); isErr {
		t.Fatalf("list_jobs %v: %s", args, text)
	}
	if out.Count != len(out.Jobs) {
		t.Errorf("count = %d for %d jobs", out.Count, len(out.Jobs))
	}
	if out.HasMore != (out.NextCursor != "") {
		t.Errorf("has_more = %v with next_cursor %q", out.HasMore, out.NextCursor)
	}
	return out
}

// An org-wide list_jobs reads past one page: has_more says the list is not
// complete and next_cursor continues it, in the same soonest-deadline-first
// order. Paging one job at a time puts a page boundary between every pair,
// including the deadline tie and the two jobs with no deadline.
func TestIntegrationMCPListJobsPaginates(t *testing.T) {
	p := newPagingFixture(t)
	f := p.f
	// Soonest deadline first, a tie broken by the newer job, then the jobs
	// with no deadline, newest first. f.jobB is tenant B's and never appears.
	want := []int64{p.overdue.ID, p.tieHigh.ID, p.tieLow.ID, p.later.ID, p.noDeadline.ID, f.jobA.ID}

	s := connect(t, f.url, f.tokenA)
	for _, limit := range []int{1, 2} {
		var got []int64
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > len(want) {
				t.Fatalf("limit %d: still paging after %d pages: %v", limit, pages, got)
			}
			args := map[string]any{"limit": limit}
			if cursor != "" {
				args["cursor"] = cursor
			}
			out := p.page(t, s, args)
			for _, j := range out.Jobs {
				got = append(got, j.ID)
			}
			if !out.HasMore {
				break
			}
			cursor = out.NextCursor
		}
		if !slices.Equal(got, want) {
			t.Errorf("limit %d: paged jobs = %v, want %v", limit, got, want)
		}
	}

	// One page that holds everything says so.
	all := p.page(t, s, map[string]any{})
	if all.HasMore || len(all.Jobs) != len(want) {
		t.Errorf("one page = %d jobs, has_more %v; want %d, false", len(all.Jobs), all.HasMore, len(want))
	}

	// Filters page too: the overdue filter still runs server-side.
	overdueOnly := p.page(t, s, map[string]any{"overdue_only": true, "limit": 1})
	if len(overdueOnly.Jobs) != 1 || overdueOnly.Jobs[0].ID != p.overdue.ID || overdueOnly.HasMore {
		t.Errorf("overdue page = %+v, want only job %d and no more", overdueOnly, p.overdue.ID)
	}
}

// The cursor carries the deadline the page was sorted on. When the last job
// of a page gets a later deadline before the next call, the next page still
// starts right after where the first one ended: no job is skipped. The moved
// job shows again at its new place.
func TestIntegrationMCPListJobsCursorSurvivesADeadlineChange(t *testing.T) {
	p := newPagingFixture(t)
	f := p.f
	s := connect(t, f.url, f.tokenA)

	first := p.page(t, s, map[string]any{"limit": 2})
	if len(first.Jobs) != 2 || first.Jobs[1].ID != p.tieHigh.ID {
		t.Fatalf("page 1 = %+v, want the overdue job then tie-high", first.Jobs)
	}
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE job_step_assignments SET deadline = $1 WHERE job_id = $2`, p.tie.Add(30*24*time.Hour), p.tieHigh.ID); err != nil {
		t.Fatalf("move tie-high's deadline: %v", err)
	}

	var rest []int64
	cursor := first.NextCursor
	for pages := 0; cursor != ""; pages++ {
		if pages > 6 {
			t.Fatalf("still paging: %v", rest)
		}
		out := p.page(t, s, map[string]any{"limit": 2, "cursor": cursor})
		for _, j := range out.Jobs {
			rest = append(rest, j.ID)
		}
		cursor = out.NextCursor
	}
	want := []int64{p.tieLow.ID, p.later.ID, p.tieHigh.ID, p.noDeadline.ID, f.jobA.ID}
	if !slices.Equal(rest, want) {
		t.Errorf("after the move, pages 2+ = %v, want %v (tie-low and later must not be skipped)", rest, want)
	}
}

// A cursor is only a position in the caller's own list: a malformed one is
// refused, never read as the first page, and a well-formed one cannot reach
// another organization's jobs.
func TestIntegrationMCPListJobsCursorValidation(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	for _, cursor := range []string{"12", "abc", "none.0", "x.12"} {
		if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{"cursor": cursor}, nil); !isErr {
			t.Errorf("cursor %q: no error, got %s", cursor, text)
		}
	}

	// A position before every job: tenant A gets its own job and never B's.
	var out jobPage
	before := jobsCursorBeforeAll()
	if isErr, text := callTool(t, s, "openloc_list_jobs", map[string]any{"cursor": before}, &out); isErr {
		t.Fatalf("cursor before every job: %s", text)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].ID != f.jobA.ID {
		t.Errorf("jobs after %q = %+v, want only tenant A's job %d", before, out.Jobs, f.jobA.ID)
	}
}

// jobsCursorBeforeAll is a cursor that sorts before any real job: a deadline
// in 1970 and the largest id.
func jobsCursorBeforeAll() string { return "0.9223372036854775807" }
