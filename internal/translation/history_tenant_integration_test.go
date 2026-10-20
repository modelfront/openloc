package translation_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Every history row carries its translation's tenant, whichever statement
// wrote it: writers never pass it, a trigger fills it.
func TestIntegrationTranslationHistory_TenantComesFromTheTranslation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	trA := seedMinimalTranslation(t, q, "tenant-a")
	trB := seedMinimalTranslation(t, q, "tenant-b")
	user := testutil.SeedUser(t, q, testutil.SeedTenant(t, q, "history-writer").ID, "writer@test.com", "pass")

	row, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{TranslationID: trA, Kind: db.TranslationHistoryKindHuman})
	if err != nil {
		t.Fatalf("insert history: %v", err)
	}
	if row.TenantID == nil {
		t.Fatal("InsertTranslationHistory returned no tenant_id")
	}
	if err := q.InsertClearedHistoryBatch(ctx, db.InsertClearedHistoryBatchParams{TranslationIds: []int64{trA, trB}, CreatedBy: user.ID}); err != nil {
		t.Fatalf("insert cleared batch: %v", err)
	}

	wrong := testutil.CountRows(t, pool, `
		SELECT count(*) FROM translation_history th
		JOIN translations t ON t.id = th.translation_id
		JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE th.tenant_id IS DISTINCT FROM p.tenant_id`)
	total := testutil.CountRows(t, pool, `SELECT count(*) FROM translation_history`)
	if total != 3 || wrong != 0 {
		t.Errorf("%d of %d history rows carry another tenant than their translation's, want 0 of 3", wrong, total)
	}
}
