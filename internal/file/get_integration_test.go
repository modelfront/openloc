package file_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// GET /api/projects/{projectID}/files/{fileID} has no ownership middleware; the
// handler's tenant-scoped lookup is the only guard, and it ignores the project
// in the path. So another tenant's file id is a 404 under the victim's project
// and under the attacker's own project alike.
func TestIntegrationFileGetCrossTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	victimUser, victim := testutil.RegisterUser(t, q, "victim")
	victimProject := testutil.SeedProject(t, q, victim, "Victim project", "en", []string{"de"})
	victimFile := testutil.SeedFile(t, q, victimProject.ID, "confidential-brief.json", db.FileFormatJsonI18n)

	attackerUser, attacker := testutil.RegisterUser(t, q, "attacker")
	attackerProject := testutil.SeedProject(t, q, attacker, "Attacker project", "en", []string{"de"})

	for _, projectID := range []int64{victimProject.ID, attackerProject.ID} {
		path := fmt.Sprintf("/api/projects/%d/files/%d", projectID, victimFile.ID)
		rec := testutil.DoRequest(t, router, http.MethodGet, path, nil, attackerUser, attacker)
		if strings.Contains(rec.Body.String(), victimFile.Name) {
			t.Errorf("GET %s leaked the file: %s", path, rec.Body.String())
		}
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}

	rec := testutil.DoRequest(t, router, http.MethodGet,
		fmt.Sprintf("/api/projects/%d/files/%d", victimProject.ID, victimFile.ID), nil, victimUser, victim)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data struct {
			ID        int64  `json:"id"`
			ProjectID int64  `json:"project_id"`
			Name      string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.ID != victimFile.ID || resp.Data.ProjectID != victimProject.ID || resp.Data.Name != victimFile.Name {
		t.Fatalf("owner GET = %+v, want id %d project %d name %q", resp.Data, victimFile.ID, victimProject.ID, victimFile.Name)
	}
}
