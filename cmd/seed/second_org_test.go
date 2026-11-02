package main

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The second org is provisioned like a real one: it has the Default library
// workflow, so a project can be created there, and the plain-text demo
// template follows it.
func TestSeedSecondOrg_HasTheDefaultWorkflow(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	q := db.New(pool)

	healLanguages(ctx, pool)
	_, userID := seedCoreData(ctx, q)
	seedSecondOrg(ctx, q, userID)
	acme, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("GetTenantBySlug(acme): %v", err)
	}
	wf, err := q.GetWorkflowByName(ctx, db.GetWorkflowByNameParams{TenantID: acme.ID, Name: "Default"})
	if err != nil {
		t.Fatalf("the second org has no Default library workflow: %v", err)
	}

	tmpl, err := healPlainTextTemplate(ctx, q, pool, acme.ID)
	if err != nil {
		t.Fatalf("plain-text template: %v", err)
	}
	profile, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
	if err != nil {
		t.Fatalf("template profile: %v", err)
	}
	if profile.WorkflowID == nil || *profile.WorkflowID != wf.ID {
		t.Errorf("plain-text template workflow = %v, want the Default workflow %d", profile.WorkflowID, wf.ID)
	}
}
