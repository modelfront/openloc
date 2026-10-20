package publicapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// The documented first-project walk-through, end to end, with an access key.
// Every response is checked against the spec by the client.
func TestIntegrationV1_ProjectFileJobFlow(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Flow")
	c, key := f.client, f.keys["project_manager"]

	rec := c.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Spring catalog", "source_language": "en", "target_languages": []string{"de", "fr"},
		"deadline": "2026-12-01T09:00:00Z",
	}, key)
	wantStatus(t, rec, http.StatusCreated)
	p := testutil.DecodeData[publicapi.Project](t, rec)
	if p.Status != "in_progress" || p.Name != "Spring catalog" || len(p.TargetLanguages) != 2 {
		t.Fatalf("created project = %+v", p)
	}
	if p.Deadline == nil || *p.Deadline != "2026-12-01T09:00:00Z" {
		t.Fatalf("deadline = %v", p.Deadline)
	}
	base := fmt.Sprintf("/api/v1/projects/%d", p.ID)
	rec = c.do(http.MethodGet, base, nil, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Project](t, rec); got.ID != p.ID || got.Name != "Spring catalog" {
		t.Fatalf("read project = %+v", got)
	}

	rec = c.do(http.MethodPatch, base, map[string]any{"name": "Spring catalog v2", "deadline": nil}, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Project](t, rec); got.Name != "Spring catalog v2" || got.Deadline != nil {
		t.Fatalf("patched project = %+v", got)
	}

	rec = c.upload(base+"/files", "catalog.txt", "Hello world.\n\nSee you soon.\n", key)
	wantStatus(t, rec, http.StatusCreated)
	up := testutil.DecodeData[publicapi.File](t, rec)
	if up.ProjectID != p.ID || up.Format != "txt" || up.SegmentCount != 2 {
		t.Fatalf("uploaded file = %+v", up)
	}

	rec = c.do(http.MethodGet, fmt.Sprintf("%s/files/%d", base, up.ID), nil, key)
	wantStatus(t, rec, http.StatusOK)

	rec = c.do(http.MethodGet, base+"/jobs", nil, key)
	wantStatus(t, rec, http.StatusOK)
	jobs := testutil.DecodeData[[]publicapi.Job](t, rec)
	if len(jobs) != 2 {
		t.Fatalf("one upload with two targets made %d jobs, want 2", len(jobs))
	}
	for _, j := range jobs {
		if j.FileID != up.ID || j.ProjectID != p.ID || j.SourceLanguage != "en" || j.Counts.Total != 2 {
			t.Fatalf("job = %+v", j)
		}
	}
	jobPath := fmt.Sprintf("/api/v1/jobs/%d", jobs[0].ID)

	rec = c.do(http.MethodGet, jobPath, nil, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[publicapi.Job](t, rec); got.ID != jobs[0].ID || got.Status != "created" {
		t.Fatalf("job detail = %+v", got)
	}

	rec = c.do(http.MethodGet, jobPath+"/export?format=xliff", nil, key)
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-xliff+xml" {
		t.Fatalf("xliff export Content-Type = %q", ct)
	}

	// A second file lists next to the first, and goes again on delete.
	rec = c.upload(base+"/files", "extra.txt", "Extra.\n", key)
	wantStatus(t, rec, http.StatusCreated)
	extra := testutil.DecodeData[publicapi.File](t, rec)
	rec = c.do(http.MethodGet, base+"/files", nil, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.File](t, rec); len(got) != 2 {
		t.Fatalf("files = %+v", got)
	}
	rec = c.do(http.MethodDelete, fmt.Sprintf("%s/files/%d", base, extra.ID), nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, base+"/files", nil, key)
	wantStatus(t, rec, http.StatusOK)
	if got := testutil.DecodeData[[]publicapi.File](t, rec); len(got) != 1 || got[0].ID != up.ID {
		t.Fatalf("files after delete = %+v", got)
	}

	rec = c.do(http.MethodGet, fmt.Sprintf("/api/v1/files/%d/source", up.ID), nil, key)
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "Hello world.\n\nSee you soon.\n" {
		t.Fatalf("source download = %q", rec.Body.String())
	}

	rec = c.do(http.MethodDelete, jobPath, nil, key)
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, jobPath, nil, key)
	wantStatus(t, rec, http.StatusNotFound)

	// A live project and its files are owner/admin to delete.
	rec = c.do(http.MethodDelete, base, nil, key)
	wantStatus(t, rec, http.StatusForbidden)
	rec = c.do(http.MethodDelete, base, nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusNoContent)
	rec = c.do(http.MethodGet, base, nil, key)
	wantStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationV1_ListsPaginate(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Pages")
	c, key := f.client, f.keys["owner"]

	// The fixture made one project; two more make three.
	for i := 0; i < 2; i++ {
		rec := c.do(http.MethodPost, "/api/v1/projects", map[string]any{
			"name": fmt.Sprintf("P%d", i), "source_language": "en", "target_languages": []string{"de"},
		}, key)
		wantStatus(t, rec, http.StatusCreated)
	}

	seen := map[int64]bool{}
	cursor := int64(0)
	for page := 0; ; page++ {
		rec := c.do(http.MethodGet, fmt.Sprintf("/api/v1/projects?limit=2&cursor=%d", cursor), nil, key)
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Data    []publicapi.Project `json:"data"`
			Cursor  int64               `json:"cursor"`
			HasMore bool                `json:"has_more"`
		}
		decodeJSON(t, rec, &body)
		for _, p := range body.Data {
			if seen[p.ID] {
				t.Fatalf("project %d on two pages", p.ID)
			}
			seen[p.ID] = true
		}
		if !body.HasMore {
			if body.Cursor != 0 {
				t.Fatalf("last page cursor = %d, want 0", body.Cursor)
			}
			break
		}
		if len(body.Data) != 2 || page > 3 {
			t.Fatalf("page %d: %d items, has_more", page, len(body.Data))
		}
		cursor = body.Cursor
	}
	if len(seen) != 3 {
		t.Fatalf("walked %d projects, want 3", len(seen))
	}

	rec := c.do(http.MethodGet, "/api/v1/projects?status=nope", nil, key)
	wantStatus(t, rec, http.StatusBadRequest)
}

// A file id is only valid under the project it belongs to.
func TestIntegrationV1_FileMustBelongToProject(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Nested")

	rec := f.client.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Other", "source_language": "en", "target_languages": []string{"de"},
	}, f.keys["owner"])
	wantStatus(t, rec, http.StatusCreated)
	other := testutil.DecodeData[publicapi.Project](t, rec)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec = f.client.do(method, fmt.Sprintf("/api/v1/projects/%d/files/%d", other.ID, f.file.ID), nil, f.keys["owner"])
		wantStatus(t, rec, http.StatusNotFound)
	}
	// The file survived the mismatched delete.
	rec = f.client.do(http.MethodGet, f.path("/projects/{projectID}/files/{fileID}"), nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusOK)
}

func TestIntegrationV1_AssignJob(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Assign")

	rec := f.client.do(http.MethodPost, f.path("/jobs/{jobID}/assign"), map[string]any{"user_id": f.users["admin"].ID}, f.keys["project_manager"])
	wantStatus(t, rec, http.StatusOK)
	j := testutil.DecodeData[publicapi.Job](t, rec)
	if j.WorkflowStep == nil || j.WorkflowStep.AssigneeID == nil || *j.WorkflowStep.AssigneeID != f.users["admin"].ID {
		t.Fatalf("assigned job step = %+v", j.WorkflowStep)
	}

	// A user from another organization is not assignable.
	other := newFixture(t, q, router, "Elsewhere")
	rec = f.client.do(http.MethodPost, f.path("/jobs/{jobID}/assign"), map[string]any{"user_id": other.users["linguist"].ID}, f.keys["owner"])
	wantStatus(t, rec, http.StatusBadRequest)
	rec = f.client.do(http.MethodGet, f.path("/jobs/{jobID}"), nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusOK)
	if j := testutil.DecodeData[publicapi.Job](t, rec); j.WorkflowStep == nil || j.WorkflowStep.AssigneeID == nil || *j.WorkflowStep.AssigneeID != f.users["admin"].ID {
		t.Fatalf("job step after the refused assign = %+v", j.WorkflowStep)
	}
}

func TestIntegrationV1_SpecIsServed(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "Spec")

	rec := f.client.do(http.MethodGet, "/api/v1/openapi.json", nil, f.keys["linguist"])
	wantStatus(t, rec, http.StatusOK)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req := httpRequest(http.MethodGet, "/api/v1/openapi.json", f.keys["linguist"])
	req.Header.Set("If-None-Match", etag)
	if got := serve(router, req); got.Code != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", got.Code)
	}

	// No key, no spec.
	if got := serve(router, httpRequest(http.MethodGet, "/api/v1/openapi.json", "")); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous spec = %d, want 401", got.Code)
	}
}
