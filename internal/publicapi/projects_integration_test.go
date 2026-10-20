package publicapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// Customer and domain are labels in v1: create refuses the app's fields for
// them, as update does, and makes no project.
func TestIntegrationV1_CreateRefusesCustomerFields(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "CreateFields")
	key := f.key(t, "owner")

	countProjects := func() int {
		rec := f.client.do(http.MethodGet, "/api/v1/projects?limit=100", nil, key)
		wantStatus(t, rec, http.StatusOK)
		return len(testutil.DecodeData[[]publicapi.Project](t, rec))
	}
	before := countProjects()

	for _, field := range []string{"client", "customer_id", "domain"} {
		body := map[string]any{"name": "With " + field, "source_language": "en", "target_languages": []string{"de"}}
		if field == "customer_id" {
			body[field] = f.project.ID
		} else {
			body[field] = "Retail"
		}
		rec := f.client.do(http.MethodPost, "/api/v1/projects", body, key)
		wantStatus(t, rec, http.StatusBadRequest)
	}
	if after := countProjects(); after != before {
		t.Fatalf("projects = %d after refused creates, want %d", after, before)
	}
}

// The job list is cursor-paged, and every job on a page has its own counts.
func TestIntegrationV1_ProjectJobsArePaged(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "JobPages")
	key := f.key(t, "owner")

	rec := f.client.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Three targets", "source_language": "en", "target_languages": []string{"de", "fr", "it"},
	}, key)
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.Project](t, rec)
	rec = f.client.upload(fmt.Sprintf("/api/v1/projects/%d/files", p.ID), "a.txt", "One.\nTwo.\n", key)
	wantStatus(t, rec, http.StatusCreated)

	type page struct {
		Data    []publicapi.Job `json:"data"`
		Cursor  int64           `json:"cursor"`
		HasMore bool            `json:"has_more"`
	}
	get := func(cursor int64) page {
		rec := f.client.do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/jobs?limit=2&cursor=%d", p.ID, cursor), nil, key)
		wantStatus(t, rec, http.StatusOK)
		var out page
		decodeJSON(t, rec, &out)
		return out
	}

	first := get(0)
	if len(first.Data) != 2 || !first.HasMore || first.Cursor != first.Data[1].ID {
		t.Fatalf("first page = %d jobs, has_more %v, cursor %d", len(first.Data), first.HasMore, first.Cursor)
	}
	second := get(first.Cursor)
	if len(second.Data) != 1 || second.HasMore || second.Cursor != 0 {
		t.Fatalf("second page = %d jobs, has_more %v, cursor %d", len(second.Data), second.HasMore, second.Cursor)
	}

	seen := map[string]bool{}
	for _, j := range append(first.Data, second.Data...) {
		seen[j.TargetLanguage] = true
		if j.ProjectID != p.ID {
			t.Fatalf("job %d project = %d, want %d", j.ID, j.ProjectID, p.ID)
		}
		if j.Counts.Total != 2 {
			t.Fatalf("job %d (%s) counts.total = %d, want 2", j.ID, j.TargetLanguage, j.Counts.Total)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("target languages over both pages = %v, want de, fr, it", seen)
	}
}

// Each job on a page carries its own active workflow step.
func TestIntegrationV1_ProjectJobPagesCarrySteps(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "JobSteps")
	key := f.key(t, "owner")
	// A second job with no step, after the fixture's job.
	second := testutil.SeedJob(t, q, f.file.ID, "fr")

	page := func(cursor int64) []publicapi.Job {
		rec := f.client.do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/jobs?limit=1&cursor=%d", f.project.ID, cursor), nil, key)
		wantStatus(t, rec, http.StatusOK)
		return testutil.DecodeData[[]publicapi.Job](t, rec)
	}
	first := page(0)
	if len(first) != 1 || first[0].ID != f.job.ID {
		t.Fatalf("first page = %+v, want job %d", first, f.job.ID)
	}
	if s := first[0].WorkflowStep; s == nil || s.AssigneeID == nil || *s.AssigneeID != f.users["linguist"].ID {
		t.Fatalf("job %d step = %+v, want the linguist's", f.job.ID, first[0].WorkflowStep)
	}
	next := page(f.job.ID)
	if len(next) != 1 || next[0].ID != second.ID || next[0].WorkflowStep != nil {
		t.Fatalf("second page = %+v, want job %d with no step", next, second.ID)
	}
}
