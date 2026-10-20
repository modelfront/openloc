package testutil

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// SeedAIEngine creates an AI engine for purpose: "mt" for an LLM translation
// engine, "qe_ape" for quality estimation and post-editing.
func SeedAIEngine(t testing.TB, q db.Querier, tenantID int64, name, purpose string) db.AiEngine {
	t.Helper()
	engine, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{TenantID: tenantID, Name: name, Purpose: purpose})
	if err != nil {
		t.Fatalf("SeedAIEngine(%q): %v", name, err)
	}
	return engine
}
