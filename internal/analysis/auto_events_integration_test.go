package analysis_test

// Emission test for the auto-analysis audit event:
// EnsurePendingAutoAnalysis records analysis.created with trigger:"auto" —
// once per coalesced pending row, not once per upload. RED without the
// projectevents.Emit call in EnsurePendingAutoAnalysis.

import (
	"context"
	"encoding/json"
	"testing"

	"openloc/internal/analysis"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationAutoAnalysisEmitsCreatedEvent(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "AutoAnalysisCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Auto Analyzed", "en", []string{"de"})

	// First call creates the pending row → one event. The second call
	// coalesces (ON CONFLICT DO NOTHING) → NO second event.
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, "en")
	analysis.EnsurePendingAutoAnalysis(ctx, q, tenant.ID, project.ID, "en")

	events, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
		ProjectID: project.ID, TenantID: tenant.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("list project events: %v", err)
	}
	var created []map[string]any
	for _, e := range events {
		if e.EventType == "analysis.created" {
			var details map[string]any
			if err := json.Unmarshal(e.Details, &details); err != nil {
				t.Fatalf("bad details json: %v", err)
			}
			created = append(created, details)
			// No auth claims on this path → system event, never a panic.
			if e.ActorID != nil {
				t.Errorf("expected nil actor for claims-less auto-analysis, got %v", *e.ActorID)
			}
		}
	}
	if len(created) != 1 {
		t.Fatalf("expected exactly 1 analysis.created event (coalesced), got %d", len(created))
	}
	if created[0]["trigger"] != "auto" {
		t.Errorf("expected trigger=auto in details, got %v", created[0]["trigger"])
	}
	if created[0]["analysis_id"] == nil {
		t.Errorf("expected analysis_id in details, got %v", created[0])
	}
}
