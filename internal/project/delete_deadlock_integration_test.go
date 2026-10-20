package project_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A project delete that loses a deadlock (an open editor writing a segment
// hold on the rows the delete cascades through) runs again and succeeds; the
// user gets a 204, not a server error.
func TestIntegrationProjectDelete_RetriesALostDeadlock(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "DeadlockCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "deadlock@test.com", "pass", "admin")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "Deadlocked Project", "source_language": "en", "target_languages": []string{"es"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct{ Data struct{ ID int64 } }
	testutil.DecodeJSON(t, rec, &created)

	// The first delete of a project fails as Postgres fails a deadlock victim
	// (SQLSTATE 40P01); later ones go through. A sequence counts the attempts:
	// unlike a row, nextval survives the failed attempt's rollback.
	for _, stmt := range []string{
		`CREATE SEQUENCE test_deadlock_once`,
		`CREATE FUNCTION test_deadlock_once() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF nextval('test_deadlock_once') = 1 THEN
				RAISE EXCEPTION 'deadlock detected' USING ERRCODE = '40P01';
			END IF;
			RETURN OLD;
		END $$`,
		`CREATE TRIGGER test_deadlock_once BEFORE DELETE ON projects FOR EACH ROW EXECUTE FUNCTION test_deadlock_once()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the deadlock: %v", err)
		}
	}
	// The database is shared with other tests: the trigger goes before the
	// pool closes.
	defer func() {
		for _, stmt := range []string{
			`DROP TRIGGER IF EXISTS test_deadlock_once ON projects`,
			`DROP FUNCTION IF EXISTS test_deadlock_once()`,
			`DROP SEQUENCE IF EXISTS test_deadlock_once`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Logf("remove the deadlock: %v", err)
			}
		}
	}()

	rec = testutil.DoRequest(t, router, "DELETE", "/api/projects/"+itoa(created.Data.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	var attempts int64
	if err := pool.QueryRow(ctx, `SELECT last_value FROM test_deadlock_once`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("delete attempts = %d (err %v), want 2: one deadlocked, one went through", attempts, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE id = $1`, created.Data.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("project rows left: %d (err %v), want 0", left, err)
	}
}
