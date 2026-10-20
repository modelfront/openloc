package publicapi_test

import (
	"context"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/publicapi"
	"openloc/internal/testutil"
)

// A job whose pre-translation found no MT engine for its language says why, so
// a client polling for completion can tell it apart from work in progress.
func TestIntegrationV1_JobCarriesMTSkipReason(t *testing.T) {
	q, router, cleanup := newRouter(t)
	defer cleanup()
	f := newFixture(t, q, router, "MTSkip")

	rec := f.client.do(http.MethodGet, f.path("/jobs/{jobID}"), nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusOK)
	if j := testutil.DecodeData[publicapi.Job](t, rec); j.MTSkipReason != nil {
		t.Fatalf("mt_skip_reason on a job that never skipped MT = %q, want null", *j.MTSkipReason)
	}

	reason := "no_route"
	if err := q.SetJobMTSkipReason(context.Background(), db.SetJobMTSkipReasonParams{ID: f.job.ID, Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	rec = f.client.do(http.MethodGet, f.path("/jobs/{jobID}"), nil, f.keys["owner"])
	wantStatus(t, rec, http.StatusOK)
	if j := testutil.DecodeData[publicapi.Job](t, rec); j.MTSkipReason == nil || *j.MTSkipReason != reason {
		t.Fatalf("mt_skip_reason = %v, want %q", j.MTSkipReason, reason)
	}
}
