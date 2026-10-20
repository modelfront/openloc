package analytics_test

import (
	"context"
	"testing"
	"time"

	"openloc/internal/analytics"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A user who belongs to two orgs edits in both. Each org's report counts only
// the edits made in that org's own projects.
func TestIntegrationOrgProductivity_CountsOnlyTheOrgsOwnProjects(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantA := testutil.SeedTenant(t, q, "OrgA")
	tenantB := testutil.SeedTenant(t, q, "OrgB")
	shared := testutil.SeedUser(t, q, tenantA.ID, "shared@test.com", "pass")
	testutil.SeedMembership(t, q, shared.ID, tenantB.ID, "member")

	edit := func(tenantID int64, text string, words int32) {
		t.Helper()
		project := testutil.SeedProject(t, q, tenantID, "P", "en", []string{"fr"})
		file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
		seg := testutil.SeedSegment(t, q, file.ID, text, 1)
		tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusApproved)
		if _, err := pool.Exec(ctx, `UPDATE segments SET word_count = $1 WHERE id = $2`, words, seg.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE translations SET edited_by = $1, editing_time_ms = 1000, updated_at = now() WHERE id = $2`,
			shared.ID, tr.ID); err != nil {
			t.Fatal(err)
		}
	}
	edit(tenantA.ID, "one two", 2)
	edit(tenantB.ID, "one two three four five", 5)

	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, c := range []struct {
		name     string
		tenantID int64
		words    int32
	}{{"A", tenantA.ID, 2}, {"B", tenantB.ID, 5}} {
		rows, err := analytics.OrgProductivity(ctx, q, c.tenantID, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("org %s: got %d rows, want 1: %+v", c.name, len(rows), rows)
		}
		if rows[0].SegmentsEdited != 1 || rows[0].WordsEdited != c.words {
			t.Errorf("org %s: segments=%d words=%d, want 1 and %d (edits in the other org leak in)",
				c.name, rows[0].SegmentsEdited, rows[0].WordsEdited, c.words)
		}
	}
}
