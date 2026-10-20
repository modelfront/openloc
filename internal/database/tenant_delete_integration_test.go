package database_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/testutil"
)

// Deleting an organization cascades through everything it owns, including
// rows that point at each other with an in-use guard: an AI engine row on a
// prompt, a vendor token on its connection, a settings profile on its TM
// profile. The guard still stops a direct delete of the thing in use.
func TestIntegrationDeleteTenantCascadesThroughInUseGuards(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "Closing Org")
	keep := testutil.SeedTenant(t, q, "Staying Org")

	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{TenantID: tenant.ID, Name: "Reviewer", PromptText: "Review.", OutStatus: true})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenant.ID, Name: "Review engine", Purpose: "qe_ape"})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if _, err := q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: langcode.Wildcard, TargetLangs: []string{langcode.Wildcard},
		SystemPromptID: &prompt.ID, Model: "gpt-5", Settings: []byte("{}"),
	}); err != nil {
		t.Fatalf("engine row: %v", err)
	}
	testutil.SeedAIVendorToken(t, q, tenant.ID, "openai", "Default", "dev-ai-secrets-key-change-me")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared memories", 0.75)
	project := testutil.SeedProject(t, q, tenant.ID, "Linked Project", "en", []string{"de"})
	if _, err := pool.Exec(ctx, `UPDATE settings_profiles SET tm_profile_id = $1 WHERE id = $2`, tp.ID, project.SettingsProfileID); err != nil {
		t.Fatalf("link tm profile: %v", err)
	}

	// The guard: a prompt an engine row uses cannot be deleted on its own.
	_, err = pool.Exec(ctx, `DELETE FROM ai_prompts WHERE id = $1`, prompt.ID)
	if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23503" {
		t.Fatalf("deleting an in-use prompt: err = %v, want a foreign key violation", err)
	}

	if err := q.DeleteTenant(ctx, tenant.ID); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM tenants WHERE id = $1) + (SELECT count(*) FROM ai_prompts WHERE tenant_id = $1)`, tenant.ID).Scan(&left); err != nil {
		t.Fatalf("count leftovers: %v", err)
	}
	if left != 0 {
		t.Errorf("%d rows of the deleted tenant are left", left)
	}
	if _, err := q.GetTenant(ctx, keep.ID); err != nil {
		t.Errorf("the other tenant is gone: %v", err)
	}
}
