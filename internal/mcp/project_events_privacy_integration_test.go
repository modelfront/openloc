package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"openloc/internal/projectevents"
)

// The history feed's details carry other people's emails and raw error text
// for the app's own rendering. The tool returns the product facts and leaves
// those out, nested values included; a failure stays visible as a flag.
func TestIntegrationMCPProjectEventsOmitContactsAndErrorText(t *testing.T) {
	f := newFixture(t)
	if err := projectevents.EmitInTx(context.Background(), f.q, projectevents.Event{
		ProjectID: f.projectA.ID, TenantID: f.tenantA.ID, Type: "job.assigned",
		ActorID: &f.ownerA.ID, ActorName: f.ownerA.Email,
		Details: map[string]any{
			"file_name":          "alpha.txt",
			"assignee_id":        f.linguistA.ID,
			"assignee_email":     f.linguistA.Email,
			"triggered_by_email": "someone@test.com",
			"error":              "dial tcp 10.0.0.7:5432: connection refused",
			"error_dismissed":    "upstream 502 from mt.internal.example",
			"changes": map[string]any{
				"owner": map[string]any{"before": "a", "after": "b", "email": "nested@test.com"},
			},
		},
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	var out struct {
		Events []struct {
			ActorName string         `json:"actor_name"`
			Details   map[string]any `json:"details"`
		} `json:"events"`
	}
	if isErr, text := callTool(t, connect(t, f.url, f.tokenA), "openloc_list_project_events",
		map[string]any{"project_id": f.projectA.ID, "type": "job.assigned"}, &out); isErr {
		t.Fatalf("list_project_events: %s", text)
	}
	if len(out.Events) != 1 {
		t.Fatalf("want one event, got %+v", out.Events)
	}
	ev := out.Events[0]
	raw, _ := json.Marshal(ev.Details)
	for _, leaked := range []string{f.linguistA.Email, f.ownerA.Email, "someone@test.com", "nested@test.com", "10.0.0.7", "mt.internal.example"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("details leak %q: %s", leaked, raw)
		}
	}
	if ev.Details["file_name"] != "alpha.txt" || ev.Details["assignee_id"] == nil {
		t.Errorf("product facts dropped: %s", raw)
	}
	// That a step failed, or a failure was dismissed, is still reported.
	if ev.Details["error"] != true || ev.Details["error_dismissed"] != true {
		t.Errorf("failure facts not kept as flags: %s", raw)
	}
	owner, _ := ev.Details["changes"].(map[string]any)["owner"].(map[string]any)
	if owner["before"] != "a" || owner["after"] != "b" {
		t.Errorf("nested change dropped: %s", raw)
	}
	if ev.ActorName != f.ownerA.Name {
		t.Errorf("actor_name = %q, want the actor's name %q", ev.ActorName, f.ownerA.Name)
	}
}
