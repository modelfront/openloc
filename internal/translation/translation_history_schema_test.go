package translation_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestTranslationHistorySchema_Shape pins the translation_history schema:
// the origin / tm_match_percent / in_flight_phase columns on
// translations, the cascade FK from translation_history, and the
// tm_match_percent CHECK. A migration that breaks the shape lands as a
// failing test rather than as runtime ambiguity.
//
// Behavioral emission rules (a history row on each write path) are covered
// by the emission integration tests.
func TestTranslationHistorySchema_Shape(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("translations carries the origin, tm_match_percent and in_flight_phase columns", func(t *testing.T) {
		// The `origin` column is of type `translation_origin`.
		rows, err := pool.Query(ctx, `
			SELECT column_name, udt_name
			FROM information_schema.columns
			WHERE table_name = 'translations'
			  AND column_name IN ('origin', 'tm_match_percent', 'in_flight_phase', 'legacy_origin')
			ORDER BY column_name`)
		if err != nil {
			t.Fatalf("query columns: %v", err)
		}
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var name, udt string
			if err := rows.Scan(&name, &udt); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got[name] = udt
		}
		want := map[string]string{
			"origin":           "translation_origin",
			"tm_match_percent": "int2",
			"in_flight_phase":  "translation_phase",
		}
		if _, exists := got["legacy_origin"]; exists {
			t.Error("translations must not carry a legacy_origin column")
		}
		for col, expectedUdt := range want {
			if got[col] != expectedUdt {
				t.Errorf("translations.%s: want udt=%q, got %q (full map: %v)", col, expectedUdt, got[col], got)
			}
		}
	})

	t.Run("translation_history table exists with cascade FK to translations", func(t *testing.T) {
		var tablePresent bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables WHERE table_name = 'translation_history'
			)`).Scan(&tablePresent); err != nil {
			t.Fatalf("query: %v", err)
		}
		if !tablePresent {
			t.Fatal("translation_history table missing")
		}

		expectedCols := []string{
			"id", "translation_id", "kind", "text", "rich_text",
			"created_by", "tm_id", "tm_entry_id", "mt_engine_id",
			"tm_match_percent", "edit_distance", "job_step_assignment_id",
			"details", "created_at",
		}
		for _, col := range expectedCols {
			var present bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_name = 'translation_history' AND column_name = $1
				)`, col).Scan(&present); err != nil {
				t.Fatalf("query column %q: %v", col, err)
			}
			if !present {
				t.Errorf("translation_history.%s missing", col)
			}
		}

		// FK to translations must be ON DELETE CASCADE so deleting a
		// translation cleans up its history.
		var deleteRule string
		if err := pool.QueryRow(ctx, `
			SELECT rc.delete_rule
			FROM information_schema.referential_constraints rc
			JOIN information_schema.table_constraints tc
			  ON tc.constraint_name = rc.constraint_name
			JOIN information_schema.constraint_column_usage ccu
			  ON ccu.constraint_name = rc.constraint_name
			WHERE tc.table_name = 'translation_history'
			  AND tc.constraint_type = 'FOREIGN KEY'
			  AND ccu.table_name = 'translations'`).Scan(&deleteRule); err != nil {
			t.Fatalf("query FK rule: %v", err)
		}
		if deleteRule != "CASCADE" {
			t.Errorf("translation_history.translation_id FK delete_rule: want CASCADE, got %q", deleteRule)
		}
	})

	t.Run("tm_match_percent CHECK pins the ICE-aware range [0,101]", func(t *testing.T) {
		trID := seedMinimalTranslation(t, q, "history-check")

		// Valid: 101 (ICE reserved slot).
		if _, err := pool.Exec(ctx, `
			INSERT INTO translation_history (translation_id, kind, text, tm_match_percent)
			VALUES ($1, 'tm', 'Hola', 101)`, trID); err != nil {
			t.Errorf("inserting tm_match_percent=101 (ICE slot) must succeed; got %v", err)
		}

		// Invalid: 102.
		if _, err := pool.Exec(ctx, `
			INSERT INTO translation_history (translation_id, kind, text, tm_match_percent)
			VALUES ($1, 'tm', 'Hola', 102)`, trID); err == nil {
			t.Error("inserting tm_match_percent=102 must violate CHECK; got nil error")
		}

		// Invalid: -1.
		if _, err := pool.Exec(ctx, `
			INSERT INTO translation_history (translation_id, kind, text, tm_match_percent)
			VALUES ($1, 'tm', 'Hola', -1)`, trID); err == nil {
			t.Error("inserting tm_match_percent=-1 must violate CHECK; got nil error")
		}
	})

	t.Run("cascade delete: removing a translation nukes its history rows", func(t *testing.T) {
		trID := seedMinimalTranslation(t, q, "history-cascade")
		if _, err := pool.Exec(ctx, `
			INSERT INTO translation_history (translation_id, kind, text)
			VALUES ($1, 'human', 'Hola')`, trID); err != nil {
			t.Fatalf("seed history: %v", err)
		}
		var before int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM translation_history WHERE translation_id = $1`, trID).Scan(&before); err != nil {
			t.Fatalf("count before: %v", err)
		}
		if before == 0 {
			t.Fatal("history row missing after insert")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM translations WHERE id = $1`, trID); err != nil {
			t.Fatalf("delete translation: %v", err)
		}
		var after int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM translation_history WHERE translation_id = $1`, trID).Scan(&after); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if after != 0 {
			t.Errorf("expected 0 history rows after cascade delete; got %d", after)
		}
	})
}

// TestTranslationHistorySchema_EveryForeignKeyIsIndexed pins that every
// foreign key of translation_history leads a valid index, so deleting a parent
// row (step assignment, TM entry, MT engine, …) does not scan the table.
func TestTranslationHistorySchema_EveryForeignKeyIsIndexed(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	if missing := testutil.UnindexedForeignKeys(t, pool, "translation_history"); len(missing) > 0 {
		t.Errorf("foreign keys of translation_history with no index: %v", missing)
	}
}

// seedMinimalTranslation creates a tenant→project→file→segment→translation
// chain and returns the translation id. Each call uses a fresh suffix to
// avoid uniqueness collisions across subtests sharing the same DB.
func seedMinimalTranslation(t *testing.T, q db.Querier, suffix string) int64 {
	t.Helper()
	tenant := testutil.SeedTenant(t, q, "history-test-"+suffix)
	project := testutil.SeedProject(t, q, tenant.ID, "history-test-project-"+suffix, "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "history-test-"+suffix+".txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	return tr.ID
}
