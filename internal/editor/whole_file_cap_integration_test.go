package editor_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/testutil"
)

// The routes that return a whole file in one response stop at
// httputil.WholeFileCap rows; a larger file is read through the editor routes,
// which still answer for it.
func TestIntegrationWholeFileRoutes_StopAtTheCap(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "whole-file-cap")
	project := testutil.SeedProject(t, q, tenantID, "Big", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "big.xliff", db.FileFormatDocx)
	job := testutil.SeedJob(t, q, file.ID, "de")

	paths := []string{
		fmt.Sprintf("/api/files/%d/segments/", file.ID),
		fmt.Sprintf("/api/files/%d/translations/?lang=de", file.ID),
		fmt.Sprintf("/api/jobs/%d/translations", job.ID),
	}
	get := func(path string) int {
		return testutil.DoRequest(t, router, http.MethodGet, path, nil, userID, tenantID).Code
	}

	testutil.SeedSegmentsBulk(t, pool, file.ID, "de", 0, httputil.WholeFileCap, "Hallo")
	for _, p := range paths {
		if code := get(p); code != http.StatusOK {
			t.Errorf("GET %s with %d segments = %d, want 200", p, httputil.WholeFileCap, code)
		}
	}

	testutil.SeedSegmentsBulk(t, pool, file.ID, "de", httputil.WholeFileCap, 1, "Hallo")
	for _, p := range paths {
		if code := get(p); code != http.StatusUnprocessableEntity {
			t.Errorf("GET %s with %d segments = %d, want 422", p, httputil.WholeFileCap+1, code)
		}
	}
	if code := get(fmt.Sprintf("/api/jobs/%d/editor/index", job.ID)); code != http.StatusOK {
		t.Errorf("the editor index of the large file = %d, want 200", code)
	}
}
