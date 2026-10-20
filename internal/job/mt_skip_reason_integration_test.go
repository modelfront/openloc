package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The job list and the single-job read carry mt_skip_reason, so the project
// page can show a job whose pre-translation found no MT engine as not done.
func TestIntegrationJobViewCarriesMTSkipReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "NoMTView")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@nomtview.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "No MT view", "en", []string{"es", "de"})
	f := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	es := testutil.SeedJob(t, q, f.ID, "es")
	de := testutil.SeedJob(t, q, f.ID, "de")
	reason := "no_route"
	if err := q.SetJobMTSkipReason(ctx, db.SetJobMTSkipReasonParams{ID: de.ID, Reason: &reason}); err != nil {
		t.Fatal(err)
	}

	type view struct {
		ID           int64   `json:"id"`
		MTSkipReason *string `json:"mt_skip_reason"`
	}
	rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/jobs", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data []view `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[int64]*string{}
	for _, v := range list.Data {
		got[v.ID] = v.MTSkipReason
	}
	if got[es.ID] != nil {
		t.Errorf("es mt_skip_reason = %q, want absent", *got[es.ID])
	}
	if got[de.ID] == nil || *got[de.ID] != reason {
		t.Errorf("de mt_skip_reason = %v, want %q", got[de.ID], reason)
	}

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d", de.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var one struct {
		Data view `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one.Data.MTSkipReason == nil || *one.Data.MTSkipReason != reason {
		t.Errorf("GET /api/jobs/%d mt_skip_reason = %v, want %q", de.ID, one.Data.MTSkipReason, reason)
	}
}
