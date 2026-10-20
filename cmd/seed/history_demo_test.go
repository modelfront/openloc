package main

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// The History demo lands in the second org, once, carrying the rows the TM
// pipeline and the confirm handler write — the shapes the editor's History
// panel groups and marks.
func TestHealHistoryDemo_SeedsOnceInSecondOrg(t *testing.T) {
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

	healHistoryDemo(ctx, q, pool, userID)
	firstRunRows := historyRowsInTenant(t, pool, acme.ID)
	healHistoryDemo(ctx, q, pool, userID)

	if got := projectsNamed(t, q, acme.ID, historyDemoProjectName); got != 1 {
		t.Fatalf("Acme has %d %q projects after two runs, want 1", got, historyDemoProjectName)
	}
	if got := historyRowsInTenant(t, pool, acme.ID); got != firstRunRows {
		t.Errorf("a second run changed Acme's history rows: %d → %d", firstRunRows, got)
	}
	// The main org's seed steps treat its newest TM and its history as the
	// Demo Project's own, so nothing of the demo may land there.
	if got := projectsNamed(t, q, mainTenantID, historyDemoProjectName); got != 0 {
		t.Errorf("main org has %d %q projects, want 0", got, historyDemoProjectName)
	}
	var tmID int64
	var entries int
	if err := pool.QueryRow(ctx, `
		SELECT tc.id, (SELECT count(*) FROM translation_memory tm WHERE tm.tm_id = tc.id)
		FROM tm_collections tc WHERE tc.tenant_id = $1 AND tc.name = $2`,
		acme.ID, historyDemoTMName).Scan(&tmID, &entries); err != nil {
		t.Fatalf("demo TM in Acme: %v", err)
	}
	if entries != len(historyDemoTM) {
		t.Errorf("demo TM has %d entries, want %d", entries, len(historyDemoTM))
	}

	// Two saves by one person in one step on top of a fuzzy match that three
	// Apply TM runs filled — newest first, as the panel reads it.
	click := historyFor(t, pool, q, acme.ID, "Click Save to keep your changes.")
	if got := kinds(click); !slices.Equal(got, []string{"human", "human", "tm", "tm", "tm"}) {
		t.Fatalf("history kinds = %v, want [human human tm tm tm]", got)
	}
	if got := ptr.Deref(click[0].Text); got != "Haga clic en Guardar para conservar sus cambios." {
		t.Errorf("newest edit = %q, want the second save", got)
	}
	for _, h := range click[:2] {
		if h.CreatedBy == nil || *h.CreatedBy != userID {
			t.Errorf("edit created_by = %v, want the seed user %d", h.CreatedBy, userID)
		}
		if ptr.Deref(h.StepName) != "Translation" {
			t.Errorf("edit step = %q, want Translation", ptr.Deref(h.StepName))
		}
	}
	for _, h := range click[2:] {
		if ptr.Deref(h.TmName) != historyDemoTMName || h.TmMatchPercent == nil || *h.TmMatchPercent != 88 {
			t.Errorf("tm row = %q %v, want %q 88%%", ptr.Deref(h.TmName), h.TmMatchPercent, historyDemoTMName)
		}
		if h.CreatedBy != nil || h.JobStepAssignmentID != nil {
			t.Errorf("tm row carries a person or step (%v, %v); the pipeline sets neither", h.CreatedBy, h.JobStepAssignmentID)
		}
	}

	// An exact match is approved by the fill itself, so a re-run leaves it be.
	if got := kinds(historyFor(t, pool, q, acme.ID, "Your report has been sent.")); !slices.Equal(got, []string{"tm"}) {
		t.Errorf("exact match history = %v, want [tm]", got)
	}
	if got := kinds(historyFor(t, pool, q, acme.ID, "The weather is nice today.")); len(got) != 0 {
		t.Errorf("unmatched segment history = %v, want none", got)
	}

	// The segment's own state agrees with its last event.
	var status string
	var activeText *string
	var editedBy *int64
	if err := pool.QueryRow(ctx, `
		SELECT t.status, t.active_text, t.edited_by FROM translations t
		JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id
		JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1 AND s.source_text = $2`,
		acme.ID, "Click Save to keep your changes.").Scan(&status, &activeText, &editedBy); err != nil {
		t.Fatalf("translation state: %v", err)
	}
	if status != "approved" || ptr.Deref(activeText) != "Haga clic en Guardar para conservar sus cambios." || editedBy == nil || *editedBy != userID {
		t.Errorf("translation = %s %q edited_by=%v, want approved, the second save, the seed user", status, ptr.Deref(activeText), editedBy)
	}
}

func projectsNamed(t *testing.T, q *db.Queries, tenantID int64, name string) int {
	t.Helper()
	projects, err := q.ListProjects(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	n := 0
	for _, p := range projects {
		if p.Name == name {
			n++
		}
	}
	return n
}

func historyRowsInTenant(t *testing.T, pool *pgxpool.Pool, tenantID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM translation_history th
		JOIN translations t ON t.id = th.translation_id JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count history: %v", err)
	}
	return n
}

func historyFor(t *testing.T, pool *pgxpool.Pool, q *db.Queries, tenantID int64, source string) []db.ListTranslationHistoryRow {
	t.Helper()
	ctx := context.Background()
	var trID int64
	if err := pool.QueryRow(ctx, `
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1 AND s.source_text = $2`, tenantID, source).Scan(&trID); err != nil {
		t.Fatalf("translation for %q: %v", source, err)
	}
	rows, err := q.ListTranslationHistory(ctx, trID)
	if err != nil {
		t.Fatalf("ListTranslationHistory(%q): %v", source, err)
	}
	return rows
}

func kinds(rows []db.ListTranslationHistoryRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = string(r.Kind)
	}
	return out
}
