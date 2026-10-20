package analysis_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Saved-analysis schema (migration 042).
// Pins the schema-level guarantees: FK delete rules, CHECK constraints (the
// auto_approved-as-subset invariant especially), the snapshot-survives-job-
// deletion rule, and the GIN index on analyses.job_ids. Worker behavior is
// covered in service_test.go.

func TestAnalysesSchema_Shape(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "analyses-schema")
	project := testutil.SeedProject(t, q, tenant.ID, "analyses-schema-project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "analyses-schema.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	t.Run("analyses table + key columns + enum present", func(t *testing.T) {
		expectedCols := map[string]string{
			"id":                "int8",
			"tenant_id":         "int8",
			"project_id":        "int8",
			"name":              "text",
			"status":            "analysis_status",
			"source_language":   "text",
			"job_ids":           "_int8", // bigint[] surfaces as _int8 in udt_name
			"settings_snapshot": "jsonb",
		}
		for col, want := range expectedCols {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT udt_name FROM information_schema.columns
				WHERE table_name='analyses' AND column_name=$1`, col).Scan(&got); err != nil {
				t.Errorf("analyses.%s missing: %v", col, err)
				continue
			}
			if got != want {
				t.Errorf("analyses.%s: want udt=%q, got %q", col, want, got)
			}
		}
	})

	t.Run("job_analysis FK delete rules: CASCADE on analysis, SET NULL on job", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT kcu.column_name, rc.delete_rule
			FROM information_schema.referential_constraints rc
			JOIN information_schema.key_column_usage kcu
			  ON kcu.constraint_name = rc.constraint_name
			WHERE kcu.table_name='job_analysis'`)
		if err != nil {
			t.Fatalf("query FK rules: %v", err)
		}
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var col, rule string
			if err := rows.Scan(&col, &rule); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got[col] = rule
		}
		if got["analysis_id"] != "CASCADE" {
			t.Errorf("job_analysis.analysis_id FK: want CASCADE, got %q (full: %v)", got["analysis_id"], got)
		}
		// SET NULL is what preserves the snapshot when the job is later deleted.
		if got["job_id"] != "SET NULL" {
			t.Errorf("job_analysis.job_id FK: want SET NULL (snapshot survival), got %q", got["job_id"])
		}
	})

	t.Run("CHECK: name must be non-empty", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language)
			VALUES ($1, $2, '', 'en')`, tenant.ID, project.ID)
		if err == nil {
			t.Error("empty name must violate analyses_name_nonempty CHECK; got nil error")
		}
	})

	t.Run("CHECK: failed status requires error_message", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, status, completed_at)
			VALUES ($1, $2, 'no-msg', 'en', 'failed', NOW())`, tenant.ID, project.ID)
		if err == nil {
			t.Error("failed without error_message must violate CHECK; got nil error")
		}
	})

	t.Run("CHECK: completed/failed both require completed_at; pending must not have it", func(t *testing.T) {
		// completed without completed_at — violates.
		_, err := pool.Exec(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, status)
			VALUES ($1, $2, 'no-ts', 'en', 'completed')`, tenant.ID, project.ID)
		if err == nil {
			t.Error("completed without completed_at must violate CHECK; got nil error")
		}
		// pending with completed_at — violates (the constraint is biconditional).
		_, err = pool.Exec(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, status, completed_at)
			VALUES ($1, $2, 'ts-on-pending', 'en', 'pending', NOW())`, tenant.ID, project.ID)
		if err == nil {
			t.Error("pending with completed_at must violate CHECK; got nil error")
		}
	})

	t.Run("CHECK: auto_approved metrics never exceed band totals", func(t *testing.T) {
		// Seed a valid analysis first.
		var analysisID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, job_ids)
			VALUES ($1, $2, 'aa-subset', 'en', ARRAY[$3::bigint])
			RETURNING id`, tenant.ID, project.ID, job.ID).Scan(&analysisID); err != nil {
			t.Fatalf("seed analysis: %v", err)
		}

		// Valid: 10 segments, 5 auto_approved — subset OK.
		_, err := pool.Exec(ctx, `
			INSERT INTO job_analysis (analysis_id, job_id, target_language, job_name_snapshot,
				tm_100_segments, tm_100_segments_auto_approved)
			VALUES ($1, $2, 'de', 'snap', 10, 5)`, analysisID, job.ID)
		if err != nil {
			t.Errorf("valid subset (5 ≤ 10) must succeed; got %v", err)
		}

		// Invalid: auto_approved > total — violates tm_100_aa_subset CHECK.
		_, err = pool.Exec(ctx, `
			INSERT INTO job_analysis (analysis_id, job_id, target_language, job_name_snapshot,
				tm_100_segments, tm_100_segments_auto_approved)
			VALUES ($1, $2, 'fr', 'snap', 3, 5)`, analysisID, job.ID)
		if err == nil {
			t.Error("auto_approved > total must violate CHECK; got nil error")
		}
	})

	t.Run("CASCADE: deleting analysis sweeps job_analysis rows", func(t *testing.T) {
		var analysisID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language)
			VALUES ($1, $2, 'cascade-test', 'en') RETURNING id`,
			tenant.ID, project.ID).Scan(&analysisID); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_analysis (analysis_id, job_id, target_language, job_name_snapshot)
			VALUES ($1, $2, 'de', 'snap')`, analysisID, job.ID); err != nil {
			t.Fatalf("seed breakdown: %v", err)
		}

		if _, err := pool.Exec(ctx, `DELETE FROM analyses WHERE id=$1`, analysisID); err != nil {
			t.Fatalf("delete analysis: %v", err)
		}

		var remaining int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM job_analysis WHERE analysis_id=$1`, analysisID).Scan(&remaining); err != nil {
			t.Fatalf("count: %v", err)
		}
		if remaining != 0 {
			t.Errorf("cascade delete failed: %d job_analysis rows remain", remaining)
		}
	})

	t.Run("SET NULL: deleting job preserves the snapshot row with job_name_snapshot", func(t *testing.T) {
		// Isolated job so this delete doesn't impact other subtests.
		snapJob := testutil.SeedJob(t, q, file.ID, "fr")

		var analysisID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language)
			VALUES ($1, $2, 'snapshot-survival', 'en') RETURNING id`,
			tenant.ID, project.ID).Scan(&analysisID); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_analysis (analysis_id, job_id, target_language, job_name_snapshot)
			VALUES ($1, $2, 'fr', 'My Important File.docx')`,
			analysisID, snapJob.ID); err != nil {
			t.Fatalf("seed breakdown: %v", err)
		}

		// Delete the job; the breakdown row must survive with job_id=NULL.
		if _, err := pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, snapJob.ID); err != nil {
			t.Fatalf("delete job: %v", err)
		}

		var jobIDOut *int64
		var nameSnap string
		if err := pool.QueryRow(ctx, `
			SELECT job_id, job_name_snapshot FROM job_analysis
			WHERE analysis_id=$1 AND target_language='fr'`,
			analysisID).Scan(&jobIDOut, &nameSnap); err != nil {
			t.Fatalf("read post-delete: %v", err)
		}
		if jobIDOut != nil {
			t.Errorf("job_id: want NULL after job delete, got %d", *jobIDOut)
		}
		if nameSnap != "My Important File.docx" {
			t.Errorf("job_name_snapshot: want preserved, got %q", nameSnap)
		}
	})

	t.Run("GIN index supports reverse lookup: list analyses including a job", func(t *testing.T) {
		// Two analyses, only one includes the target job.
		var a1, a2 int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, job_ids)
			VALUES ($1, $2, 'with-job', 'en', ARRAY[$3::bigint, 999999::bigint])
			RETURNING id`, tenant.ID, project.ID, job.ID).Scan(&a1); err != nil {
			t.Fatalf("seed a1: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO analyses (tenant_id, project_id, name, source_language, job_ids)
			VALUES ($1, $2, 'without-job', 'en', ARRAY[888888::bigint])
			RETURNING id`, tenant.ID, project.ID).Scan(&a2); err != nil {
			t.Fatalf("seed a2: %v", err)
		}

		rows, err := pool.Query(ctx, `
			SELECT id FROM analyses
			WHERE project_id=$1 AND job_ids @> ARRAY[$2::bigint]`,
			project.ID, job.ID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var got []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got = append(got, id)
		}

		// Earlier subtests may have also seeded analyses against this job, so
		// assert membership rather than exact equality: a1 IS in the result,
		// a2 (the one with a different job id) IS NOT.
		seen := map[int64]bool{}
		for _, id := range got {
			seen[id] = true
		}
		if !seen[a1] {
			t.Errorf("reverse lookup: a1=%d should be in result, got %v", a1, got)
		}
		if seen[a2] {
			t.Errorf("reverse lookup: a2=%d should NOT be in result (its job_ids has no overlap), got %v", a2, got)
		}
	})
}
