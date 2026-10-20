package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"openloc/internal/auth"
	"openloc/internal/chat"
	"openloc/internal/demoorg"
)

// In the demo organization every saved tour is complete: each step's pick
// finds an item, so no step is left out. A playbook added
// with a pick the demo dataset cannot answer fails here.
func TestIntegrationDemoOrgRunsEverySavedTourInFull(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	demoID, _, err := demoorg.Ensure(ctx, f.pool, time.Now())
	if err != nil {
		t.Fatalf("build the demo org: %v", err)
	}
	owner, err := f.q.GetTenantOwner(ctx, demoID)
	if err != nil {
		t.Fatal(err)
	}
	guide := owner.ID
	req := httptest.NewRequest(http.MethodGet, "/api/chat/playbooks", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		TenantID: demoID, UserID: guide, Role: auth.RoleOwner,
		Demo: true, RealUserID: f.ownerA.ID, RealTenantID: f.tenantA.ID,
	}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playbooks: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []struct {
			Name  string            `json:"name"`
			Steps []json.RawMessage `json:"steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	want := chat.SavedTourLengths()
	seen := 0
	for _, pb := range env.Data {
		n, ok := want[pb.Name]
		if !ok {
			continue
		}
		seen++
		if len(pb.Steps) != n {
			t.Errorf("%s: %d of %d steps in the demo org", pb.Name, len(pb.Steps), n)
		}
	}
	if seen != len(want) {
		t.Errorf("the guide sees %d of %d saved tours", seen, len(want))
	}
}
