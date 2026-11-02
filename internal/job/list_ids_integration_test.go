package job_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// GET /api/projects/{id}/jobs?ids=… returns only those jobs of the project:
// the project page polls just its running jobs. A job of another project (or
// another org) named in ids is not returned.
func TestIntegrationListJobsByProject_IDsFilter(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "JobIDsCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@jobids.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "IDs", "en", []string{"es", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	es := testutil.SeedJob(t, q, file.ID, "es")
	testutil.SeedJob(t, q, file.ID, "fr")
	otherProject := testutil.SeedProject(t, q, tenant.ID, "Other", "en", []string{"es"})
	otherJob := testutil.SeedJob(t, q, testutil.SeedFile(t, q, otherProject.ID, "b.txt", db.FileFormatTxt).ID, "es")

	get := func(t *testing.T, query string) (int, []int64) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs%s", project.ID, query), nil, user.ID, tenant.ID)
		if rec.Code != http.StatusOK {
			return rec.Code, nil
		}
		var resp struct {
			Data []struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		ids := make([]int64, len(resp.Data))
		for i, j := range resp.Data {
			ids[i] = j.ID
		}
		return rec.Code, ids
	}

	if _, ids := get(t, ""); len(ids) != 2 {
		t.Fatalf("no filter: %v, want both jobs", ids)
	}
	if _, ids := get(t, fmt.Sprintf("?ids=%d", es.ID)); len(ids) != 1 || ids[0] != es.ID {
		t.Errorf("ids=%d: got %v, want only that job", es.ID, ids)
	}
	if _, ids := get(t, fmt.Sprintf("?ids=%d,%d", es.ID, otherJob.ID)); len(ids) != 1 || ids[0] != es.ID {
		t.Errorf("a job of another project must not be returned: got %v", ids)
	}
	// Another org naming this project's job ids gets nothing.
	strangerTenant := testutil.SeedTenant(t, q, "JobIDsOther")
	stranger := testutil.SeedUser(t, q, strangerTenant.ID, "pm@jobidsother.test", "pass")
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs?ids=%d", project.ID, es.ID), nil, stranger.ID, strangerTenant.ID)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), `"id"`) {
		t.Errorf("another org: status %d, body %s; want 404 with no job", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"?ids=abc", "?ids=0", "?ids=-1", "?ids=1,,2"} {
		if code, _ := get(t, bad); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
}
