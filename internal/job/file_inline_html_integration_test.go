package job_test

// The job response says whether its file's strings are HTML
// (file_inline_html): the CAT editor reads it there, for every role, to load
// and save a plain-text file as typed. Both files are checked, so a field
// that is always true or always false fails one of them.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationJobCarriesFileInlineHTML(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "JobInlineHTML")
	user := testutil.SeedUser(t, q, tenant.ID, "u@job-inline.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Inline HTML jobs", "en", []string{"fr"})

	want := map[int64]bool{}
	for _, on := range []bool{true, false} {
		f, err := q.CreateFile(context.Background(), db.CreateFileParams{
			ProjectID: project.ID, Name: fmt.Sprintf("strings-%v.json", on), Format: db.FileFormatJsonI18n,
			StoragePath: fmt.Sprintf("test/strings-%v.json", on), InlineHtml: &on,
		})
		if err != nil {
			t.Fatal(err)
		}
		want[testutil.SeedJob(t, q, f.ID, "fr").ID] = on
	}

	type jobBody struct {
		ID             int64 `json:"id"`
		FileInlineHTML *bool `json:"file_inline_html"`
	}
	check := func(t *testing.T, where string, j jobBody) {
		t.Helper()
		if j.FileInlineHTML == nil {
			t.Fatalf("%s: job %d has no file_inline_html", where, j.ID)
		}
		if *j.FileInlineHTML != want[j.ID] {
			t.Errorf("%s: job %d file_inline_html = %v, want %v", where, j.ID, *j.FileInlineHTML, want[j.ID])
		}
	}

	t.Run("get", func(t *testing.T) {
		for id := range want {
			rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d", id), nil, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var resp struct {
				Data jobBody `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &resp)
			check(t, "GET /api/jobs/{id}", resp.Data)
		}
	})

	t.Run("list", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs", project.ID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []jobBody `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if len(resp.Data) != len(want) {
			t.Fatalf("listed %d jobs, want %d", len(resp.Data), len(want))
		}
		for _, j := range resp.Data {
			check(t, "GET /api/projects/{id}/jobs", j)
		}
	})
}
