package testutil

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// CountWorkflowsNamed counts the tenant's live library workflows called name.
func CountWorkflowsNamed(t testing.TB, q db.Querier, tenantID int64, name string) int {
	t.Helper()
	wfs, err := q.ListWorkflows(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	n := 0
	for _, wf := range wfs {
		if wf.Name == name {
			n++
		}
	}
	return n
}

// CountAIPromptsNamed counts the tenant's AI prompts called name.
func CountAIPromptsNamed(t testing.TB, q db.Querier, tenantID int64, name string) int {
	t.Helper()
	prompts, err := q.ListAIPrompts(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("list AI prompts: %v", err)
	}
	n := 0
	for _, p := range prompts {
		if p.Name == name {
			n++
		}
	}
	return n
}
