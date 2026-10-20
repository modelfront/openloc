// Project tools called by tenant B with tenant A's project, term base or label
// value id. Each call is refused as not found and leaves tenant A's rows as
// they were; the same calls with the caller's own ids go through, so each
// refusal is the tenant check and not a malformed call.
package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func TestIntegrationMCPProjectToolsRejectOtherTenantIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	draftA := seedDraftProject(t, f.q, f.tenantA.ID, "Tenant A draft")
	baseA := testutil.SeedTermBase(t, f.q, f.tenantA.ID, "Tenant A glossary", "en", "de")
	baseB := testutil.SeedTermBase(t, f.q, f.tenantB.ID, "Tenant B glossary", "en", "fr")
	valueA := systemLabelValue(t, f, f.tenantA.ID, "customer", "Tenant A customer")
	termBases := func(projectID, baseID int64, target string) map[string]any {
		return map[string]any{"project_id": projectID, "term_bases": []map[string]any{
			{"term_base_id": baseID, "source_language": "en", "target_language": target},
		}}
	}
	listFiles := map[string]any{"project_id": f.projectA.ID}
	publish := map[string]any{"project_id": draftA.ID}
	deleteValue := map[string]any{"value_id": valueA.ID}

	// Each term base call names one foreign id and tenant B's own other one.
	asB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	mustNotFindAll(t, asB, []foreignCall{
		{"openloc_list_files", listFiles},
		{"openloc_set_project_term_bases", termBases(f.projectA.ID, baseB.ID, "de")},
		{"openloc_set_project_term_bases", termBases(f.projectB.ID, baseA.ID, "fr")},
		{"openloc_publish_project", publish},
		{"openloc_delete_label_value", deleteValue},
	})

	for _, p := range []db.Project{f.projectA, f.projectB} {
		if rows, err := f.q.ListSettingsTermAssignments(ctx, p.SettingsProfileID); err != nil || len(rows) != 0 {
			t.Errorf("project %q got term bases: %+v (err %v)", p.Name, rows, err)
		}
	}
	if p, err := f.q.GetProject(ctx, db.GetProjectParams{ID: draftA.ID, TenantID: f.tenantA.ID}); err != nil || p.Status != db.ProjectStatusDraft {
		t.Errorf("tenant A's draft is no longer a draft: %s (err %v)", p.Status, err)
	}
	var deleted bool
	if err := f.pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM label_values WHERE id = $1", valueA.ID).Scan(&deleted); err != nil || deleted {
		t.Errorf("tenant A's label value was deleted (err %v)", err)
	}

	asA := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	var files struct {
		Files []fileOut `json:"files"`
	}
	if isErr, text := callTool(t, asA, "openloc_list_files", listFiles, &files); isErr || len(files.Files) != 1 || files.Files[0].Name != "alpha.txt" {
		t.Errorf("list_files as tenant A: isErr=%v %q %+v", isErr, text, files)
	}
	for _, own := range []struct {
		s       sessionT
		project db.Project
		base    int64
		target  string
		who     string
	}{
		{asA, f.projectA, baseA.ID, "de", "tenant A"},
		{asB, f.projectB, baseB.ID, "fr", "tenant B"},
	} {
		if isErr, text := callTool(t, own.s, "openloc_set_project_term_bases", termBases(own.project.ID, own.base, own.target), nil); isErr {
			t.Fatalf("set_project_term_bases as %s: %s", own.who, text)
		}
		rows, err := f.q.ListSettingsTermAssignments(ctx, own.project.SettingsProfileID)
		if err != nil || len(rows) != 1 || rows[0].TermBaseID != own.base {
			t.Errorf("%s's own term base not stored: %+v (err %v)", own.who, rows, err)
		}
	}
	var published struct {
		Status string `json:"status"`
	}
	if isErr, text := callTool(t, asA, "openloc_publish_project", publish, &published); isErr || published.Status != "in_progress" {
		t.Errorf("publish_project as tenant A: isErr=%v %q %+v", isErr, text, published)
	}
	g := cfgPreviewOnly(t, asA, "openloc_delete_label_value", deleteValue)
	if !strings.Contains(g.Preview.Summary, "Tenant A customer") {
		t.Errorf("delete_label_value preview should name the value: %+v", g.Preview)
	}
}
