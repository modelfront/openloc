package project_test

import (
	"context"
	"net/http"
	"testing"

	"openloc/internal/project"
	"openloc/internal/testutil"
)

// A project create is one transaction: a setup refused after the row is
// written (another org's TM or term base in the assignment lists, which only
// the assignment write checks) leaves no project, settings profile or
// workflow step behind. The same request with the caller's own TM and term
// base is the positive control.
func TestIntegrationProjectCreate_RefusedSetupLeavesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "AtomicCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	ownTM := testutil.SeedTM(t, q, tenant.ID, "Own TM")
	ownBase := testutil.SeedTermBase(t, q, tenant.ID, "Own base", "en", "fr")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	foreignTM := testutil.SeedTM(t, q, other.ID, "Foreign TM")
	foreignBase := testutil.SeedTermBase(t, q, other.ID, "Foreign base", "en", "fr")

	create := func(body map[string]any) int {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, http.MethodPost, "/api/projects", body, pm.ID, tenant.ID, "project_manager").Code
	}
	body := func(name string, tmID, termBaseID int64) map[string]any {
		return map[string]any{
			"name": name, "source_language": "en", "target_languages": []string{"fr"},
			"tm_assignments": []map[string]any{{"tm_id": tmID, "mode": "read", "priority": 1}},
			"term_assignments": []map[string]any{{
				"term_base_id": termBaseID, "source_language": "en", "target_language": "fr", "mode": "read", "priority": 1,
			}},
		}
	}
	counts := func() (projects, profiles, steps int64) {
		t.Helper()
		projects = testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, tenant.ID)
		profiles = testutil.CountRows(t, pool, `SELECT count(*) FROM settings_profiles WHERE tenant_id = $1`, tenant.ID)
		steps = testutil.CountRows(t, pool,
			`SELECT count(*) FROM workflow_steps ws JOIN settings_profiles sp ON sp.id = ws.settings_profile_id WHERE sp.tenant_id = $1`, tenant.ID)
		return projects, profiles, steps
	}

	p0, sp0, ws0 := counts()
	for _, tc := range []struct {
		name             string
		tmID, termBaseID int64
	}{
		{"foreign TM", foreignTM.ID, ownBase.ID},
		{"foreign term base", ownTM.ID, foreignBase.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := create(body(tc.name, tc.tmID, tc.termBaseID)); code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", code)
			}
			if p, sp, ws := counts(); p != p0 || sp != sp0 || ws != ws0 {
				t.Errorf("a refused create left rows behind: projects %d→%d, settings profiles %d→%d, workflow steps %d→%d",
					p0, p, sp0, sp, ws0, ws)
			}
		})
	}

	if code := create(body("Own", ownTM.ID, ownBase.ID)); code != http.StatusCreated {
		t.Fatalf("own TM and term base: status = %d, want 201", code)
	}
	if p, _, _ := counts(); p != p0+1 {
		t.Errorf("own create: projects %d→%d, want one more", p0, p)
	}
}

// A create with a template is one transaction too: when applying the template
// fails after the project row is written, nothing is left behind and the
// caller gets the error. The template stamp is made to fail with a trigger, so
// the failure lands inside the apply, past every pre-check.
func TestIntegrationProjectCreateWithTemplate_FailedApplyLeavesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TemplateAtomicCorp")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Template")
	counts := func() (projects, profiles, events int64) {
		t.Helper()
		projects = testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, tenant.ID)
		profiles = testutil.CountRows(t, pool, `SELECT count(*) FROM settings_profiles WHERE tenant_id = $1`, tenant.ID)
		events = testutil.CountRows(t, pool, `SELECT count(*) FROM project_events WHERE tenant_id = $1`, tenant.ID)
		return projects, profiles, events
	}
	params := project.CreateParams{Name: "With template", SourceLanguage: "en", TargetLanguages: []string{"fr"}}

	// The package's tests share one database: the trigger and its function go
	// with the test, also when it fails before dropping the trigger itself.
	// Deferred after the pool's own cleanup, so it runs first.
	defer func() {
		if _, err := pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS refuse_template_stamp ON projects;
			DROP FUNCTION IF EXISTS refuse_template_stamp();`); err != nil {
			t.Errorf("drop the test trigger: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION refuse_template_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'template stamp refused'; END $$;
		CREATE TRIGGER refuse_template_stamp BEFORE UPDATE OF template_id ON projects
		FOR EACH ROW WHEN (NEW.template_id IS NOT NULL) EXECUTE FUNCTION refuse_template_stamp();`); err != nil {
		t.Fatal(err)
	}
	p0, sp0, ev0 := counts()
	if _, err := project.CreateWithTemplate(ctx, pool, q, tenant.ID, pm.ID, params, tmpl.ID); err == nil {
		t.Fatal("create with a failing template apply succeeded")
	}
	if p, sp, ev := counts(); p != p0 || sp != sp0 || ev != ev0 {
		t.Errorf("a failed apply left rows behind: projects %d→%d, settings profiles %d→%d, events %d→%d", p0, p, sp0, sp, ev0, ev)
	}

	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_template_stamp ON projects`); err != nil {
		t.Fatal(err)
	}
	created, err := project.CreateWithTemplate(ctx, pool, q, tenant.ID, pm.ID, params, tmpl.ID)
	if err != nil {
		t.Fatalf("create with the template: %v", err)
	}
	if created.TemplateID == nil || *created.TemplateID != tmpl.ID {
		t.Errorf("template_id = %v, want %d", created.TemplateID, tmpl.ID)
	}
}
