package chat_test

import (
	"context"
	"fmt"
	"testing"

	"openloc/internal/testutil"
)

// Picks for prompts and dashboards open this
// organization's items: never another tenant's, never a deleted or personal
// one, and the organization's own prompt before a built-in.
func TestIntegrationChatTourPicksOpenOwnItems(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertID := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := f.pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	prompt := func(tenantID int64, name string, isDefault, status, text, comments bool) int64 {
		return insertID(`INSERT INTO ai_prompts (tenant_id, name, prompt_text, out_status, out_text, out_comments, is_default)
			VALUES ($1, $2, 'x', $3, $4, $5, $6) RETURNING id`, tenantID, name, status, text, comments, isDefault)
	}
	dashboard := func(tenantID int64, name string, ownerID *int64) int64 {
		return insertID(`INSERT INTO dashboards (tenant_id, name, owner_id) VALUES ($1, $2, $3) RETURNING id`, tenantID, name, ownerID)
	}
	chart := func(tenantID, dashboardID int64, position int) int64 {
		return insertID(`INSERT INTO dashboard_charts (tenant_id, dashboard_id, title, definition, viz, position)
			VALUES ($1, $2, 'Chart', '{}', '{}', $3) RETURNING id`, tenantID, dashboardID, position)
	}

	prompt(f.tenantA.ID, "Built-in MT", true, false, true, false)
	customMT := prompt(f.tenantA.ID, "Own MT", false, false, true, false)
	prompt(f.tenantA.ID, "Built-in review", true, true, true, false)
	// The review prompt with a language rule shows the most, so the tour opens
	// it even though a newer one exists.
	ruledAPE := prompt(f.tenantA.ID, "Ruled APE", false, false, true, true)
	exec(`INSERT INTO ai_prompt_language_additions (prompt_id, target_languages, addition_text) VALUES ($1, '{de}', 'Use du.')`, ruledAPE)
	prompt(f.tenantA.ID, "Own APE", false, false, true, true)

	shared := dashboard(f.tenantA.ID, "Shared", nil)
	deletedChart := chart(f.tenantA.ID, shared, 0)
	exec(`UPDATE dashboard_charts SET deleted_at = now() WHERE id = $1`, deletedChart)
	firstChart := chart(f.tenantA.ID, shared, 1)
	chart(f.tenantA.ID, shared, 2)
	// A personal dashboard with more charts is not a tour's to open.
	personal := dashboard(f.tenantA.ID, "Mine", &f.ownerA.ID)
	for i := range 4 {
		chart(f.tenantA.ID, personal, i)
	}

	// Tenant B's items are newer and bigger; tenant A must never get them.
	prompt(f.tenantB.ID, "Other MT", false, false, true, false)
	prompt(f.tenantB.ID, "Other APE", false, false, true, true)
	otherDash := dashboard(f.tenantB.ID, "Other", nil)
	for i := range 5 {
		chart(f.tenantB.ID, otherDash, i)
	}

	owner := f.playbooksAs(t, f.tenantA.ID, f.ownerA.ID, "owner")
	want := []struct {
		playbook, anchor, path string
	}{
		{"tour-ai-prompts", "mt_prompt.form", fmt.Sprintf("/mt/prompts/%d", customMT)},
		{"tour-ai-prompts", "ai_prompt.instructions", fmt.Sprintf("/ai/prompts/%d", ruledAPE)},
		{"tour-dashboards", "dashboard.charts", fmt.Sprintf("/dashboards/%d", shared)},
		{"tour-dashboards", "dashboard_chart.chart", fmt.Sprintf("/dashboards/%d/charts/%d", shared, firstChart)},
	}
	for _, w := range want {
		if p, ok := stepPath(owner[w.playbook], w.anchor); p != w.path {
			t.Errorf("%s %s path = %q (present %v), want %q", w.playbook, w.anchor, p, ok, w.path)
		}
	}

	// With only built-ins, the prompt picks fall back to them.
	tenantC := testutil.SeedTenant(t, f.q, "ChatTenantC")
	ownerC := testutil.SeedUserWithRole(t, f.q, tenantC.ID, "chat-c@test.com", "pass", "owner")
	builtInMT := prompt(tenantC.ID, "Built-in MT", true, false, true, false)
	empty := f.playbooksAs(t, tenantC.ID, ownerC.ID, "owner")
	if p, _ := stepPath(empty["tour-ai-prompts"], "mt_prompt.form"); p != fmt.Sprintf("/mt/prompts/%d", builtInMT) {
		t.Errorf("built-in MT prompt path = %q, want /mt/prompts/%d", p, builtInMT)
	}
	// Nothing else to pick: the steps inside an item are left out.
	for _, gone := range []struct{ playbook, anchor string }{
		{"tour-ai-prompts", "ai_prompt.instructions"},
		{"tour-dashboards", "dashboard.charts"},
		{"tour-dashboards", "dashboard_chart.chart"},
	} {
		if _, ok := stepPath(empty[gone.playbook], gone.anchor); ok {
			t.Errorf("%s: the %s step must be left out when the organization has no item", gone.playbook, gone.anchor)
		}
	}
	if _, ok := stepPath(empty["tour-dashboards"], "dashboards.grid"); !ok {
		t.Error("tour-dashboards keeps its list step when there is no dashboard to open")
	}
}
