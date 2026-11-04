package main

import (
	"context"
	"slices"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The deadline demo lands in the second org once, and the project list's
// Deadline filter finds one of its projects in every band.
func TestHealDeadlineDemo_OneProjectPerBand(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	q := db.New(pool)

	healLanguages(ctx, pool)
	mainTenantID, userID := seedCoreData(ctx, q)
	seedSecondOrg(ctx, q, userID)
	acme, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("GetTenantBySlug(acme): %v", err)
	}

	healDeadlineDemo(ctx, q, pool, userID)
	healDeadlineDemo(ctx, q, pool, userID)

	demoNames := map[string]bool{}
	for _, d := range deadlineDemoProjects {
		demoNames[d.name] = true
		if got := projectsNamed(t, q, acme.ID, d.name); got != 1 {
			t.Errorf("Acme has %d %q projects after two runs, want 1", got, d.name)
		}
		if got := projectsNamed(t, q, mainTenantID, d.name); got != 0 {
			t.Errorf("main org has %d %q projects, want 0", got, d.name)
		}
	}

	for band, want := range map[string][]string{
		"overdue":     {"Deadline demo: overdue", "Deadline demo: overdue, on hold"},
		"approaching": {"Deadline demo: due today"},
		"soon":        {"Deadline demo: due this week"},
		"far":         {"Deadline demo: due later"},
		"none":        {"Deadline demo: no deadline"},
	} {
		rows, err := q.ListProjectsFiltered(ctx, db.ListProjectsFilteredParams{
			TenantID: acme.ID, DeadlineBands: []string{band}, Lim: 100,
		})
		if err != nil {
			t.Fatalf("ListProjectsFiltered(%s): %v", band, err)
		}
		var got []string
		for _, r := range rows {
			if demoNames[r.Name] {
				got = append(got, r.Name)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("band %s: demo projects %v, want %v", band, got, want)
		}
	}
}
