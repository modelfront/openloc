package devseed_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/devseed"
	"openloc/internal/seedid"
	"openloc/internal/testutil"
)

// A seeded project lands each job in its scenario's state, with the planned
// translations, and seeding again rebuilds it in place.
func TestIntegrationSeedBuildsScenariosAndIsIdempotent(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, seedid.OrgName)
	testutil.SeedUserWithRole(t, q, tenant.ID, seedid.OwnerEmail, "pw", "owner")
	testutil.SeedUserWithRole(t, q, tenant.ID, seedid.VendorEmail, "pw", "linguist")

	spec := devseed.Spec{
		ProjectName: "Seed test", TargetLangs: []string{"de", "fr"}, Steps: devseed.FourSteps, Translations: true,
		Files: []devseed.File{{Name: "a.txt", Segments: devseed.Sentences(0, 4), Scenarios: []devseed.Scenario{
			devseed.WithText(devseed.Finished, devseed.TextPlan{Approved: 3, AutoApproved: 1}),
			devseed.WithText(devseed.AIFailed, devseed.TextPlan{Approved: 1, Filled: 2}),
		}}},
	}
	first, err := devseed.Seed(ctx, pool, spec)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	second, err := devseed.Seed(ctx, pool, spec)
	if err != nil {
		t.Fatalf("Seed again: %v", err)
	}
	if second.ProjectID != first.ProjectID {
		t.Errorf("reseeding moved the project: %d → %d", first.ProjectID, second.ProjectID)
	}

	files, err := q.ListFilesByProject(ctx, first.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d after reseeding, want 1", len(files))
	}
	jobs, err := q.ListJobsByFile(ctx, files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	byLang := map[string]db.Job{}
	for _, j := range jobs {
		byLang[j.TargetLanguage] = j
	}

	done := byLang["de"]
	if done.Status != "completed" || done.CurrentStepID != nil {
		t.Errorf("finished job: status=%q current=%v, want completed with no current task", done.Status, done.CurrentStepID)
	}
	failed := byLang["fr"]
	if failed.Status != "failed" || failed.ErrorMessage == nil || *failed.ErrorMessage != devseed.FailedAIStepError {
		t.Errorf("failed job: status=%q error=%v", failed.Status, failed.ErrorMessage)
	}
	steps, err := q.ListJobStepAssignments(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"completed", "in_progress", "pending", "pending"}
	if len(steps) != len(want) {
		t.Fatalf("failed job has %d steps, want %d", len(steps), len(want))
	}
	for i, s := range steps {
		if s.Status != want[i] {
			t.Errorf("failed job step %d = %q, want %q", i, s.Status, want[i])
		}
	}
	if failed.CurrentStepID == nil || *failed.CurrentStepID != steps[1].ID {
		t.Errorf("failed job current task = %v, want the AI step %d", failed.CurrentStepID, steps[1].ID)
	}

	counts := func(lang string) map[string]int {
		rows, err := pool.Query(ctx,
			`SELECT t.status::text || CASE WHEN t.active_text IS NULL THEN ':empty' ELSE '' END
			 FROM translations t JOIN segments s ON s.id = t.segment_id
			 WHERE s.file_id = $1 AND t.target_language = $2`, files[0].ID, lang)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			out[k]++
		}
		return out
	}
	if got := counts("de"); got["approved"] != 3 || got["auto_approved"] != 1 || len(got) != 2 {
		t.Errorf("finished job translations = %v, want 3 approved + 1 auto_approved", got)
	}
	if got := counts("fr"); got["approved"] != 1 || got["needs_review"] != 1 || got["needs_review:empty"] != 2 {
		t.Errorf("failed job translations = %v, want 1 approved, 1 draft, 2 untranslated", got)
	}
}

// The project lands in the seed org even when an older tenant has a lower id,
// as on a database seeded before the seed org was renamed.
func TestIntegrationSeedUsesTheSeedOrgNotTheOldestTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	older := testutil.SeedTenant(t, q, "Older Org")
	testutil.SeedUserWithRole(t, q, older.ID, "someone@older.example.com", "pw", "owner")
	tenant := testutil.SeedTenant(t, q, seedid.OrgName)
	testutil.SeedUserWithRole(t, q, tenant.ID, seedid.OwnerEmail, "pw", "owner")

	res, err := devseed.Seed(ctx, pool, devseed.Spec{
		ProjectName: "Seed org test", TargetLangs: []string{"de"}, Steps: devseed.FourSteps, Translations: true,
		Files: []devseed.File{{Name: "a.txt", Segments: devseed.Sentences(0, 2), Scenarios: []devseed.Scenario{
			devseed.WithText(devseed.Finished, devseed.TextPlan{Approved: 2}),
		}}},
	})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if res.TenantID != tenant.ID {
		t.Errorf("project seeded into tenant %d, want the seed org %d", res.TenantID, tenant.ID)
	}
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: res.ProjectID, TenantID: tenant.ID}); err != nil {
		t.Errorf("project %d not found in the seed org: %v", res.ProjectID, err)
	}
}

// A scenario's tasks carry the hand-off notes a real run leaves: the reviewing
// scenario's translator comment on the AI step, which passes it on to Review,
// and the reviewer's comment on the signing-off scenario's last task. The
// editor and the Tasks tab show them on the seeded jobs.
func TestIntegrationSeedScenariosCarryHandOffNotes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, seedid.OrgName)
	testutil.SeedUserWithRole(t, q, tenant.ID, seedid.OwnerEmail, "pw", "owner")

	for _, tc := range []struct {
		scenario devseed.Scenario
		want     []string // each step's note, in order
	}{
		{devseed.Reviewing, []string{"", devseed.ReviewHandOffNote, devseed.ReviewHandOffNote, ""}},
		{devseed.SigningOff, []string{"", "", "", devseed.SignOffHandOffNote}},
	} {
		t.Run(tc.scenario.Name, func(t *testing.T) {
			res, err := devseed.Seed(ctx, pool, devseed.Spec{
				ProjectName: "Seed hand-off " + tc.scenario.Name, TargetLangs: []string{"de"}, Steps: devseed.FourSteps,
				Files: []devseed.File{{Name: "a.txt", Segments: devseed.Sentences(0, 2),
					Scenarios: []devseed.Scenario{tc.scenario}}},
			})
			if err != nil {
				t.Fatalf("Seed: %v", err)
			}
			files, err := q.ListFilesByProject(ctx, res.ProjectID)
			if err != nil || len(files) != 1 {
				t.Fatalf("files = %v, err = %v", files, err)
			}
			jobs, err := q.ListJobsByFile(ctx, files[0].ID)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs = %v, err = %v", jobs, err)
			}
			steps, err := q.ListJobStepAssignments(ctx, jobs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != len(tc.want) {
				t.Fatalf("job has %d steps, want %d", len(steps), len(tc.want))
			}
			for i, s := range steps {
				note := ""
				if s.HandoffNote != nil {
					note = *s.HandoffNote
				}
				if note != tc.want[i] {
					t.Errorf("step %d (%s) hand-off note = %q, want %q", i, s.Status, note, tc.want[i])
				}
			}
		})
	}
}
