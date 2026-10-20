package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type playbookListItem struct {
	Name    string `json:"name"`
	HasTour bool   `json:"has_tour"`
	Steps   []struct {
		Path   string `json:"path"`
		Anchor string `json:"anchor"`
		Title  string `json:"title"`
	} `json:"steps"`
}

func (f *chatFixture) playbooks(t *testing.T, role string) map[string]playbookListItem {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/chat/playbooks", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: f.tenantA.ID, UserID: f.ownerA.ID, Role: role}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d %s", role, rec.Code, rec.Body.String())
	}
	var env struct {
		Data []playbookListItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	out := map[string]playbookListItem{}
	for _, p := range env.Data {
		out[p.Name] = p
	}
	return out
}

// The list is filtered by role, and a saved tour arrives validated with its
// routes built, so the browser can run it without the model.
func TestIntegrationChatPlaybookListByRole(t *testing.T) {
	f := newChatFixture(t, nil)

	owner := f.playbooks(t, "owner")
	overview, ok := owner["demo-overview"]
	if !ok || len(overview.Steps) == 0 {
		t.Fatalf("owner should get demo-overview with its tour: %+v", owner)
	}
	if p, ok := stepPath(overview, "projects.list"); !ok || p != "/projects" {
		t.Errorf("projects.list step should open /projects, got %q (present: %v)", p, ok)
	}

	ling := f.playbooks(t, "linguist")
	if _, ok := ling["demo-overview"]; ok {
		t.Error("linguist must not get the manager demo")
	}
	if _, ok := ling["tour-ai-prompts"]; ok {
		t.Error("linguist must not get the prompts tour (the AI pages are for managers)")
	}
	if _, ok := ling["tour-assistant-and-mcp"]; !ok {
		t.Error("linguist should get the assistant and MCP tour")
	}
}

func (f *chatFixture) playbooksAs(t *testing.T, tenantID, userID int64, role string) map[string]playbookListItem {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/chat/playbooks", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: userID, Role: role}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []playbookListItem `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	out := map[string]playbookListItem{}
	for _, p := range env.Data {
		out[p.Name] = p
	}
	return out
}

func stepPath(p playbookListItem, anchor string) (string, bool) {
	for _, s := range p.Steps {
		if s.Anchor == anchor {
			return s.Path, true
		}
	}
	return "", false
}

// Picked steps open this organization's real items; another tenant's are
// never chosen.
func TestIntegrationChatPlaybookPicksRealItems(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE projects SET status = 'in_progress' WHERE id = $1`, f.project.ID); err != nil {
		t.Fatal(err)
	}
	file := testutil.SeedFile(t, f.q, f.project.ID, "guide.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, f.q, file.ID, "de")
	// A busier project in tenant B must not be picked for tenant A.
	other := testutil.SeedProject(t, f.q, f.tenantB.ID, "Other tenant", "en", []string{"fr"})
	if _, err := f.pool.Exec(ctx, `UPDATE projects SET status = 'in_progress' WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	otherFile := testutil.SeedFile(t, f.q, other.ID, "other.txt", db.FileFormatTxt)
	testutil.SeedJob(t, f.q, otherFile.ID, "fr")

	owner := f.playbooksAs(t, f.tenantA.ID, f.ownerA.ID, "owner")["demo-overview"]
	wantEditor := fmt.Sprintf("/projects/%d/jobs/%d/edit", f.project.ID, job.ID)
	if p, ok := stepPath(owner, "editor.segments"); !ok || p != wantEditor {
		t.Errorf("editor.segments path = %q (present %v), want %q", p, ok, wantEditor)
	}
}

// Every step served to an organization can show there: a customer org never
// gets a step on an element that exists only in the demo organization's
// example conversation.
func TestIntegrationChatPlaybooksOutsideTheDemoServeNoDemoOnlyStep(t *testing.T) {
	f := newChatFixture(t, nil)
	list := f.playbooksAs(t, f.tenantA.ID, f.ownerA.ID, "owner")
	tour, ok := list["tour-assistant-and-mcp"]
	if !ok || len(tour.Steps) == 0 {
		t.Fatalf("tour-assistant-and-mcp missing or empty: %+v", tour)
	}
	for _, s := range tour.Steps {
		if strings.HasPrefix(s.Anchor, "chat.example.") {
			t.Errorf("a customer org is served the demo-only step %s", s.Anchor)
		}
	}
}

// Saved tours run in the Playground: with it off, a playbook with a saved
// tour is not listed, so the UI offers none.
func TestIntegrationChatPlaybooksListToursOnlyWithThePlayground(t *testing.T) {
	f := newChatFixture(t, nil)
	f.handler.SetPlayground(false)
	if _, ok := f.playbooks(t, "owner")["demo-overview"]; ok {
		t.Error("demo-overview is listed with the Playground off")
	}
	f.handler.SetPlayground(true)
	if !f.playbooks(t, "owner")["demo-overview"].HasTour {
		t.Error("demo-overview lost its tour with the Playground on")
	}
}

// The report playbook is listed only on a deployment that takes reports.
func TestIntegrationChatReportPlaybookListedOnlyWithIssueReports(t *testing.T) {
	for _, on := range []bool{true, false} {
		f := newChatFixtureWith(t, nil, on)
		if _, listed := f.playbooks(t, "linguist")["report-an-issue"]; listed != on {
			t.Errorf("issue reports %v: listed = %v", on, listed)
		}
	}
}
