package label_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"openloc/internal/label"
	"openloc/internal/testutil"
)

// ReplaceProjectPicks checks the project's tenant itself, on the caller's
// querier: another tenant's project id is refused before any write, and that
// project's picks stay as they were. The owner's own call is the positive
// control.
func TestIntegrationReplaceProjectPicks_RefusesAnotherTenantsProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()

	router := testutil.NewTestRouter(pool)
	victimOwner, victim := testutil.RegisterUser(t, q, "victim")
	attackerOwner, attacker := testutil.RegisterUser(t, q, "attacker")
	labelValue := func(userID, tenantID int64, name, value string) int64 {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/labels", map[string]any{
			"name": name, "values": []string{value},
		}, userID, tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var created labelResp
		testutil.DecodeJSON(t, rec, &created)
		return created.Data.Values[0].ID
	}
	victimValue := labelValue(victimOwner, victim, "Country", "Germany")
	attackerValue := labelValue(attackerOwner, attacker, "Region", "North")
	project := testutil.SeedProject(t, q, victim, "Victim project", "en", []string{"fr"})

	if err := label.ReplaceProjectPicks(ctx, q, victim, project.ID, []int64{victimValue}); err != nil {
		t.Fatalf("the owner's own picks: %v", err)
	}
	picks := func(valueID int64) int64 {
		t.Helper()
		return testutil.CountRows(t, pool,
			"SELECT count(*) FROM project_labels WHERE project_id = $1 AND label_value_id = $2", project.ID, valueID)
	}
	if n := picks(victimValue); n != 1 {
		t.Fatalf("the owner's pick: %d rows, want 1", n)
	}

	err := label.ReplaceProjectPicks(ctx, q, attacker, project.ID, []int64{attackerValue})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("another tenant's project: err = %v, want pgx.ErrNoRows", err)
	}
	if n := picks(victimValue); n != 1 {
		t.Errorf("the refused call removed the owner's pick: %d rows, want 1", n)
	}
	if n := picks(attackerValue); n != 0 {
		t.Errorf("the refused call wrote its own pick: %d rows, want 0", n)
	}
}
