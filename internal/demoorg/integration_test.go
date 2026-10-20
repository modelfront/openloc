package demoorg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demoorg/demoid"
	"openloc/internal/metrics"
	"openloc/internal/testutil"
)

// The real time, not a fixed date: the QA run stamps its findings with the
// database clock, so charts read at a fixed past date miss them.
var buildTime = time.Now().UTC().Truncate(time.Second)

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func TestIntegrationEnsureBuildsOnceThenRebuildsWhenStale(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	// A customer organization, which Ensure must never touch.
	customer := testutil.SeedTenant(t, q, "Customer Org")
	testutil.SeedProject(t, q, customer.ID, "Customer Project", "en", []string{"fr"})

	first, rebuilt, err := Ensure(ctx, pool, buildTime)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if !rebuilt {
		t.Fatal("first Ensure reported no build")
	}
	translations := count(t, pool, `SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1`, first)
	if translations == 0 {
		t.Fatal("the demo organization has no translations")
	}

	// A day later nothing changes.
	again, rebuilt, err := Ensure(ctx, pool, buildTime.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if rebuilt || again != first {
		t.Fatalf("second Ensure: rebuilt=%v id=%d, want no rebuild of %d", rebuilt, again, first)
	}
	if got := count(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, first); got != 4 {
		t.Errorf("after the no-op Ensure the demo has %d projects, want 4", got)
	}

	// A new dataset version rebuilds: new tenant, old one gone.
	datasetVersion = DatasetVersion + 1
	t.Cleanup(func() { datasetVersion = DatasetVersion })
	bumped, rebuilt, err := Ensure(ctx, pool, buildTime.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Ensure after version bump: %v", err)
	}
	if !rebuilt || bumped == first {
		t.Fatalf("version bump: rebuilt=%v id=%d, want a new tenant replacing %d", rebuilt, bumped, first)
	}
	if got := count(t, pool, `SELECT count(*) FROM tenants WHERE id = $1`, first); got != 0 {
		t.Error("the old demo tenant still exists after the rebuild")
	}

	// Older than MaxAge rebuilds too; just younger does not.
	younger, rebuilt, err := Ensure(ctx, pool, buildTime.Add(48*time.Hour+MaxAge-time.Hour))
	if err != nil || rebuilt || younger != bumped {
		t.Fatalf("Ensure just inside MaxAge: id=%d rebuilt=%v err=%v, want %d unchanged", younger, rebuilt, err, bumped)
	}
	aged, rebuilt, err := Ensure(ctx, pool, buildTime.Add(48*time.Hour+MaxAge+time.Hour))
	if err != nil || !rebuilt || aged == bumped {
		t.Fatalf("Ensure past MaxAge: id=%d rebuilt=%v err=%v, want a rebuild", aged, rebuilt, err)
	}

	if got := count(t, pool, `SELECT count(*) FROM tenants WHERE kind = 'demo'`); got != 1 {
		t.Errorf("%d demo tenants, want 1", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM projects WHERE tenant_id = $1`, customer.ID); got != 1 {
		t.Errorf("the customer organization has %d projects, want its 1", got)
	}
	// The synthetic users are reused across rebuilds, not duplicated.
	if got := count(t, pool, `SELECT count(*) FROM users WHERE email LIKE '%@demo.example.com'`); got != len(members) {
		t.Errorf("%d synthetic users, want %d", got, len(members))
	}
}

func TestIntegrationDemoOrganizationContents(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenantID, _, err := Ensure(ctx, pool, buildTime)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	tenant, err := q.GetTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("get demo tenant: %v", err)
	}
	if tenant.Kind != "demo" || tenant.Domain != nil || tenant.OpenRegistration || tenant.Status != "approved" {
		t.Errorf("demo tenant fields: %+v", tenant)
	}
	if demo, err := q.GetDemoTenant(ctx); err != nil || demo.ID != tenantID {
		t.Errorf("GetDemoTenant = %d, %v; want %d", demo.ID, err, tenantID)
	}
	guide, err := q.GetTenantOwner(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetTenantOwner: %v", err)
	}
	if guide.Email != demoid.GuideEmail {
		t.Errorf("demo tenant owner = %s, want the guide %s", guide.Email, demoid.GuideEmail)
	}
	guideID := guide.ID

	t.Run("no keys", func(t *testing.T) {
		if n := count(t, pool, `SELECT count(*) FROM ai_vendor_tokens tk JOIN ai_vendors v ON v.id = tk.vendor_id WHERE v.tenant_id = $1`, tenantID); n != 0 {
			t.Errorf("%d vendor tokens in the demo organization, want 0", n)
		}
		if n := count(t, pool, `SELECT count(*) FROM mt_engines WHERE tenant_id = $1 AND (api_key IS NOT NULL OR token_id IS NOT NULL)`, tenantID); n != 0 {
			t.Errorf("%d MT engines with a key, want 0", n)
		}
		if n := count(t, pool, `SELECT count(*) FROM ai_engine_rows r JOIN ai_engines e ON e.id = r.engine_id WHERE e.tenant_id = $1 AND r.token_id IS NOT NULL`, tenantID); n != 0 {
			t.Errorf("%d AI engine rows with a token, want 0", n)
		}
	})

	t.Run("synthetic members", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT u.email, u.password_hash IS NULL, m.role,
			       (SELECT count(*) FROM memberships o WHERE o.user_id = u.id AND o.tenant_id <> $1)
			FROM users u JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $1`, tenantID)
		if err != nil {
			t.Fatalf("list members: %v", err)
		}
		defer rows.Close()
		roles := map[string]int{}
		for rows.Next() {
			var email, role string
			var noPassword bool
			var elsewhere int
			if err := rows.Scan(&email, &noPassword, &role, &elsewhere); err != nil {
				t.Fatalf("scan member: %v", err)
			}
			roles[role]++
			if !strings.HasSuffix(email, "@demo.example.com") || !noPassword {
				t.Errorf("member %s: must be on demo.example.com with no password", email)
			}
			if elsewhere != 0 {
				t.Errorf("member %s belongs to %d other organizations", email, elsewhere)
			}
		}
		if roles["owner"] != 1 || roles["project_manager"] != 1 || roles["linguist"] != 2 {
			t.Errorf("member roles = %v, want 1 owner, 1 project_manager, 2 linguists", roles)
		}
		if n := count(t, pool, `SELECT count(*) FROM user_language_pairs p JOIN memberships m ON m.user_id = p.user_id WHERE m.tenant_id = $1 AND m.role = 'linguist'`, tenantID); n < 2 {
			t.Errorf("linguists have %d language pairs, want at least 2", n)
		}
	})

	// Each page's main list, and each tour pick, has something in it.
	for _, c := range []struct {
		name string
		sql  string
		min  int
	}{
		{"shared dashboards with 4+ charts", `SELECT count(*) FROM dashboards d WHERE d.tenant_id = $1 AND d.owner_id IS NULL AND d.deleted_at IS NULL AND (SELECT count(*) FROM dashboard_charts c WHERE c.dashboard_id = d.id AND c.deleted_at IS NULL) >= 4`, 1},
		{"guide's personal dashboard", `SELECT count(*) FROM dashboards WHERE tenant_id = $1 AND owner_id = :guide AND deleted_at IS NULL`, 1},
		{"guide's notifications", `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND user_id = :guide`, 3},
		{"unread notifications for the guide", `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND user_id = :guide AND NOT read`, 1},
		{"emails pending", `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND NOT emailed`, 0},
		{"notification rules", `SELECT count(*) FROM org_notification_rules WHERE tenant_id = $1`, 1},
		{"completed analyses with a breakdown", `SELECT count(*) FROM analyses a WHERE a.tenant_id = $1 AND a.status = 'completed' AND EXISTS (SELECT 1 FROM job_analysis ja WHERE ja.analysis_id = a.id)`, 2},
		{"templates", `SELECT count(*) FROM templates WHERE tenant_id = $1`, 2},
		{"workflows", `SELECT count(*) FROM workflows WHERE tenant_id = $1 AND deleted_at IS NULL`, 3},
		{"workflow with an AI step", `SELECT count(*) FROM workflows w WHERE w.tenant_id = $1 AND EXISTS (SELECT 1 FROM workflow_definition_steps s JOIN step_definitions d ON d.id = s.step_definition_id WHERE s.workflow_id = w.id AND d.step_type IN ('hybrid', 'ai'))`, 1},
		{"step library", `SELECT count(*) FROM step_definitions WHERE tenant_id = $1 AND deleted_at IS NULL`, 4},
		{"TM profiles", `SELECT count(*) FROM tm_profiles WHERE tenant_id = $1 AND deleted_at IS NULL`, 1},
		{"MT engines", `SELECT count(*) FROM mt_engines WHERE tenant_id = $1 AND deleted_at IS NULL`, 1},
		{"MT router rules", `SELECT count(*) FROM mt_router_rules r JOIN mt_routers m ON m.id = r.router_id WHERE m.tenant_id = $1`, 1},
		{"AI router rules", `SELECT count(*) FROM ai_router_rules r JOIN ai_routers m ON m.id = r.router_id WHERE m.tenant_id = $1`, 1},
		{"AI engines", `SELECT count(*) FROM ai_engines WHERE tenant_id = $1 AND deleted_at IS NULL`, 3},
		{"MT prompt", `SELECT count(*) FROM ai_prompts WHERE tenant_id = $1 AND out_text AND NOT out_status`, 1},
		{"AI review prompt", `SELECT count(*) FROM ai_prompts WHERE tenant_id = $1 AND out_status`, 1},
		{"QA profiles", `SELECT count(*) FROM qa_profiles WHERE tenant_id = $1`, 1},
		{"label values", `SELECT count(*) FROM label_values v JOIN label_definitions d ON d.id = v.label_definition_id WHERE d.tenant_id = $1`, 4},
		{"members", `SELECT count(*) FROM memberships WHERE tenant_id = $1`, 4},
		{"TMs with entries", `SELECT count(*) FROM tm_collections c WHERE c.tenant_id = $1 AND EXISTS (SELECT 1 FROM translation_memory m WHERE m.tm_id = c.id)`, 2},
		{"linguist tasks", `SELECT count(*) FROM jobs j JOIN job_step_assignments a ON a.id = j.current_step_id JOIN memberships m ON m.user_id = a.assigned_to AND m.tenant_id = $1 JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND a.status = 'in_progress' AND m.role = 'linguist'`, 2},
		{"guide tasks", `SELECT count(*) FROM jobs j JOIN job_step_assignments a ON a.id = j.current_step_id JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND a.status = 'in_progress' AND a.assigned_to = :guide`, 1},
		{"future deadlines on live tasks", `SELECT count(*) FROM jobs j JOIN job_step_assignments a ON a.id = j.current_step_id JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND a.status = 'in_progress' AND a.deadline > :now`, 4},
		{"overdue live tasks", `SELECT count(*) FROM jobs j JOIN job_step_assignments a ON a.id = j.current_step_id JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND a.status = 'in_progress' AND a.deadline <= :now + interval '30 days'`, 0},
		{"files on the on-hold project", `SELECT count(*) FROM files f JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND p.status = 'on_hold'`, 1},
		{"AI comments", `SELECT count(*) FROM segment_comments c JOIN translations t ON t.id = c.translation_id JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND c.user_id IS NULL`, 1},
		{"AI outcomes", `SELECT count(DISTINCT t.predict_outcome) FROM translations t JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND t.predict_outcome IS NOT NULL`, 4},
		{"history over the last 30 days, distinct days", `SELECT count(DISTINCT date_trunc('day', h.created_at)) FROM translation_history h JOIN translations t ON t.id = h.translation_id JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1 AND h.created_at > :now - interval '30 days'`, 15},
		{"job timings", `SELECT count(*) FROM job_timings WHERE tenant_id = $1`, 8},
		{"AI usage", `SELECT count(*) FROM ai_usage_events WHERE tenant_id = $1 AND created_at > :now - interval '30 days'`, 3},
	} {
		sql, args := c.sql, []any{tenantID}
		if strings.Contains(sql, ":guide") {
			args = append(args, guideID)
			sql = strings.ReplaceAll(sql, ":guide", fmt.Sprintf("$%d::bigint", len(args)))
		}
		if strings.Contains(sql, ":now") {
			args = append(args, buildTime)
			sql = strings.ReplaceAll(sql, ":now", fmt.Sprintf("$%d::timestamptz", len(args)))
		}
		if got := count(t, pool, sql, args...); (c.min == 0 && got != 0) || got < c.min {
			t.Errorf("%s: %d, want %s %d", c.name, got, map[bool]string{true: "exactly", false: "at least"}[c.min == 0], c.min)
		}
	}

	t.Run("every project has QA findings or history", func(t *testing.T) {
		rows, err := pool.Query(ctx, `
			SELECT p.name, p.status,
			       (SELECT count(*) FROM project_events e WHERE e.project_id = p.id),
			       (SELECT count(*) FROM qa_results r JOIN translations t ON t.id = r.translation_id JOIN segments s ON s.id = t.segment_id JOIN files f ON f.id = s.file_id WHERE f.project_id = p.id)
			FROM projects p WHERE p.tenant_id = $1`, tenantID)
		if err != nil {
			t.Fatalf("list projects: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, status string
			var events, findings int
			if err := rows.Scan(&name, &status, &events, &findings); err != nil {
				t.Fatalf("scan project: %v", err)
			}
			if events < 2 {
				t.Errorf("project %q has %d history events", name, events)
			}
			if status == "in_progress" && findings == 0 {
				t.Errorf("in-progress project %q has no QA findings", name)
			}
		}
	})

	t.Run("tour picks resolve", func(t *testing.T) {
		if _, err := q.PickTourJob(ctx, tenantID); err != nil {
			t.Errorf("active_job: %v", err)
		}
		for _, purpose := range []string{"mt", "qe_ape"} {
			if _, err := q.PickTourPrompt(ctx, db.PickTourPromptParams{TenantID: tenantID, Purpose: purpose}); err != nil {
				t.Errorf("%s prompt: %v", purpose, err)
			}
		}
		if _, err := q.PickTourDashboard(ctx, tenantID); err != nil {
			t.Errorf("latest_dashboard: %v", err)
		}
	})

	t.Run("every chart has data", func(t *testing.T) {
		assertChartsHaveData(t, pool, q, tenantID, guideID)
	})
}

// assertChartsHaveData runs every chart the guide can see through the metrics
// engine at the build time, the way the dashboard page does.
func assertChartsHaveData(t *testing.T, pool *pgxpool.Pool, q db.Querier, tenantID, guideID int64) {
	t.Helper()
	ctx := context.Background()
	dashboards, err := q.ListDashboards(ctx, db.ListDashboardsParams{TenantID: tenantID, UserID: &guideID})
	if err != nil {
		t.Fatalf("list dashboards: %v", err)
	}
	charts := 0
	for _, d := range dashboards {
		list, err := q.ListDashboardCharts(ctx, db.ListDashboardChartsParams{DashboardID: d.ID, TenantID: tenantID, UserID: &guideID})
		if err != nil {
			t.Fatalf("list charts of %q: %v", d.Name, err)
		}
		for _, c := range list {
			var def struct {
				Measure    string            `json:"measure"`
				Dimensions []string          `json:"dimensions"`
				Filters    map[string]string `json:"filters"`
				LastDays   int               `json:"last_days"`
			}
			if err := json.Unmarshal(c.Definition, &def); err != nil {
				t.Fatalf("chart %q definition: %v", c.Title, err)
			}
			from, to, err := metrics.RangeFromLastDays(def.LastDays, buildTime)
			if err != nil {
				t.Fatalf("chart %q range: %v", c.Title, err)
			}
			res, err := metrics.Run(ctx, pool, tenantID, metrics.Request{Measure: def.Measure, Dimensions: def.Dimensions, Filters: def.Filters, From: from, To: to})
			if err != nil {
				t.Errorf("chart %q on %q: %v", c.Title, d.Name, err)
				continue
			}
			t.Logf("chart %q on %q: %d rows", c.Title, d.Name, len(res.Rows))
			if len(res.Rows) == 0 {
				t.Errorf("chart %q on %q has no data", c.Title, d.Name)
			}
			charts++
		}
	}
	if charts < 11 {
		t.Errorf("the guide sees %d charts, want at least 11", charts)
	}
}
