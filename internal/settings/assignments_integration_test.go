package settings_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// TestIntegrationSetTMAssignments_AtomicRollback pins that a failing row rolls
// back the whole replace: if any row fails to INSERT, the DELETE that cleared
// the previous assignments rolls back too and the original row survives.
//
// Distinguishes rolled-back vs committed via priority: the ORIGINAL has priority=99;
// the failed PUT has priority=5 on the valid row. If we see priority=99 after,
// the transaction rolled back correctly. If priority=5 or zero rows, the
// DELETE committed before the failed INSERT.
func TestIntegrationSetTMAssignments_AtomicRollback(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AtomicCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "atomic@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Atomic Project", "en", []string{"fr"})
	tm := testutil.SeedTM(t, q, tenant.ID, "Good TM")

	// Seed ORIGINAL assignment with priority=99 as a fingerprint.
	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/tms", project.ID), map[string]any{
		"assignments": []map[string]any{
			{"tm_id": tm.ID, "source_language": "en", "target_language": "fr", "mode": "read_write", "priority": 99},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Now PUT a list where the FIRST row is valid (priority=5 — distinct from original)
	// and the SECOND row points at a non-existent TM (FK violation).
	const nonExistentTMID = 99999999
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/tms", project.ID), map[string]any{
		"assignments": []map[string]any{
			{"tm_id": tm.ID, "source_language": "en", "target_language": "fr", "mode": "read_write", "priority": 5},
			{"tm_id": nonExistentTMID, "source_language": "en", "target_language": "fr", "mode": "read_write", "priority": 10},
		},
	}, user.ID, tenant.ID)
	t.Logf("failed PUT returned status %d body=%s", rec.Code, rec.Body.String())
	if rec.Code == http.StatusOK {
		t.Fatal("expected PUT to fail because the second row references a non-existent TM, but got 200 OK")
	}

	// The critical assertion: after the failed PUT, the ORIGINAL (priority=99) assignment
	// should still exist. If we see priority=5 or zero rows, the bug is reproduced.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var after struct {
		Data struct {
			TMAssignments []struct {
				TmID     int64 `json:"tm_id"`
				Priority int32 `json:"priority"`
			} `json:"tm_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &after)
	t.Logf("after failed PUT: %d assignments: %+v", len(after.Data.TMAssignments), after.Data.TMAssignments)
	if len(after.Data.TMAssignments) != 1 {
		t.Fatalf("after failed PUT: expected 1 TM assignment (original preserved via rollback), got %d", len(after.Data.TMAssignments))
	}
	if after.Data.TMAssignments[0].Priority != 99 {
		t.Errorf("after failed PUT: expected original priority=99 preserved via rollback, got priority=%d — the DELETE committed before the failed INSERT", after.Data.TMAssignments[0].Priority)
	}
	if after.Data.TMAssignments[0].TmID != tm.ID {
		t.Errorf("after failed PUT: expected original TM %d preserved, got %d", tm.ID, after.Data.TMAssignments[0].TmID)
	}
}

// TestIntegrationSetTermAssignments_AtomicRollback mirrors the test for term bases.
func TestIntegrationSetTermAssignments_AtomicRollback(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AtomicCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "atomic@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Atomic Project", "en", []string{"fr"})
	tb := testutil.SeedTermBase(t, q, tenant.ID, "Good Term Base", "en", "fr")

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/terminology", project.ID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": tb.ID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 0},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	const nonExistentTermBaseID = 99999999
	rec = testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/projects/%d/terminology", project.ID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": tb.ID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 0},
			{"term_base_id": nonExistentTermBaseID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 1},
		},
	}, user.ID, tenant.ID)
	if rec.Code == http.StatusOK {
		t.Fatal("expected PUT to fail, got 200 OK")
	}

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/projects/%d/settings", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var after struct {
		Data struct {
			TermAssignments []struct {
				TermBaseID int64 `json:"term_base_id"`
			} `json:"term_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &after)
	if len(after.Data.TermAssignments) != 1 {
		t.Fatalf("after failed PUT: expected 1 term assignment preserved, got %d", len(after.Data.TermAssignments))
	}
	if after.Data.TermAssignments[0].TermBaseID != tb.ID {
		t.Errorf("after failed PUT: expected original term base %d preserved, got %d", tb.ID, after.Data.TermAssignments[0].TermBaseID)
	}
}
