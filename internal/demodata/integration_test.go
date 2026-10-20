package demodata_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// tenantCounts is every table the dataset writes to, counted for one tenant,
// so "nothing else changed" is an assertion rather than a hope.
type tenantCounts map[string]int

func countTenant(t *testing.T, pool *pgxpool.Pool, tenantID int64) tenantCounts {
	t.Helper()
	queries := map[string]string{
		"projects":         `SELECT count(*) FROM projects WHERE tenant_id = $1`,
		"files":            `SELECT count(*) FROM files f JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1`,
		"jobs":             `SELECT count(*) FROM jobs j JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1`,
		"tm_collections":   `SELECT count(*) FROM tm_collections WHERE tenant_id = $1`,
		"term_bases":       `SELECT count(*) FROM term_bases WHERE tenant_id = $1`,
		"qa_profiles":      `SELECT count(*) FROM qa_profiles WHERE tenant_id = $1`,
		"ai_engines":       `SELECT count(*) FROM ai_engines WHERE tenant_id = $1`,
		"ai_prompts":       `SELECT count(*) FROM ai_prompts WHERE tenant_id = $1`,
		"customers":        `SELECT count(*) FROM customers WHERE tenant_id = $1`,
		"label_defs":       `SELECT count(*) FROM label_definitions WHERE tenant_id = $1`,
		"label_values":     `SELECT count(*) FROM label_values v JOIN label_definitions d ON d.id = v.label_definition_id WHERE d.tenant_id = $1`,
		"demo_items":       `SELECT count(*) FROM demo_workspace_items WHERE tenant_id = $1`,
		"step_assignments": `SELECT count(*) FROM job_step_assignments a JOIN jobs j ON j.id = a.job_id JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id WHERE p.tenant_id = $1`,
	}
	out := tenantCounts{}
	for name, sql := range queries {
		var n int
		if err := pool.QueryRow(context.Background(), sql, tenantID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		out[name] = n
	}
	return out
}

func assertSameCounts(t *testing.T, label string, before, after tenantCounts) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: %s changed from %d to %d", label, k, v, after[k])
		}
	}
}

func userNames(t *testing.T, pool *pgxpool.Pool) map[int64]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, name FROM users`)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		out[id] = name
	}
	return out
}

func TestIntegrationCreateDemoWorkspace(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenantA := testutil.SeedTenant(t, q, "Demo Tenant A")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "demo-a@test.com", "pass", "owner")
	linguistA := testutil.SeedUserWithRole(t, q, tenantA.ID, "demo-l@test.com", "pass", "linguist")

	// A second tenant with content of its own, which must come out untouched.
	tenantB := testutil.SeedTenant(t, q, "Demo Tenant B")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "demo-b@test.com", "pass", "owner")
	testutil.SeedProject(t, q, tenantB.ID, "Bravo Project", "en", []string{"fr"})
	testutil.SeedTM(t, q, tenantB.ID, "Bravo TM")
	beforeB := countTenant(t, pool, tenantB.ID)
	namesBefore := userNames(t, pool)

	res, err := demodata.Create(ctx, pool, tenantA.ID, ownerA.ID, demodata.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.AlreadyPresent {
		t.Fatal("first Create reported AlreadyPresent")
	}

	// --- What landed in tenant A ---
	if len(res.Projects) != 4 {
		t.Fatalf("want 4 projects, got %d: %+v", len(res.Projects), res.Projects)
	}
	for _, p := range res.Projects {
		got, err := q.GetProject(ctx, db.GetProjectParams{ID: p.ID, TenantID: tenantA.ID})
		if err != nil {
			t.Fatalf("project %d not readable in tenant A: %v", p.ID, err)
		}
		labels, err := q.ListProjectLabels(ctx, db.ListProjectLabelsParams{ProjectID: p.ID, TenantID: tenantA.ID})
		if err != nil {
			t.Fatalf("labels of %q: %v", got.Name, err)
		}
		byName := map[string]string{}
		for _, l := range labels {
			byName[l.LabelName] = l.LabelValue
		}
		if byName[demodata.DemoLabelName] != demodata.DemoLabelValue {
			t.Errorf("project %q does not carry the Demo label: %v", got.Name, byName)
		}
		if byName["Customer"] == "" || byName["Domain"] == "" {
			t.Errorf("project %q has no Customer/Domain label pick: %v", got.Name, byName)
		}
		// The customer is read through the built-in label, the way /predict does.
		pick, err := q.GetProjectSystemLabelValue(ctx, db.GetProjectSystemLabelValueParams{
			ProjectID: p.ID, TenantID: tenantA.ID, SystemKey: ptr.To("customer"),
		})
		customer := pick.Value
		if err != nil || customer == "" || !strings.HasPrefix(got.Name, strings.Fields(customer)[0]) {
			t.Errorf("project %q: customer label = %q (err %v), want the customer the name starts with", got.Name, customer, err)
		}
	}
	booking := res.Projects[0]
	if len(booking.JobIDs) != 4 {
		t.Fatalf("booking project: want 4 jobs (2 files × de/fr), got %v", booking.JobIDs)
	}
	var findings int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM qa_results r
		JOIN translations tr ON tr.id = r.translation_id
		JOIN segments s ON s.id = tr.segment_id
		JOIN jobs j ON j.file_id = s.file_id AND j.target_language = tr.target_language
		WHERE j.id = $1 AND r.check_type = 'number_consistency'`, booking.JobIDs[0]).Scan(&findings); err != nil {
		t.Fatalf("count qa findings: %v", err)
	}
	if findings == 0 {
		t.Error("the QA run inside the transaction found no number mismatch on the 24/48 hours segment")
	}
	if len(res.TranslationMemories) != 2 || len(res.TermBases) != 1 || len(res.QAProfiles) != 1 {
		t.Errorf("want 2 TMs, 1 term base, 1 QA profile; got %+v", res)
	}

	// No vendor token → no AI engines, and the result says why.
	if len(res.AIEngines) != 0 {
		t.Errorf("created AI engines without a provider connection: %+v", res.AIEngines)
	}
	if n := countSkipped(res, "AI review engine"); n != 2 {
		t.Errorf("want 2 AI engine skips, got %d: %v", n, res.Skipped)
	}
	if res.LinguistUserID != linguistA.ID {
		t.Errorf("linguist steps went to user %d, want the tenant's linguist %d", res.LinguistUserID, linguistA.ID)
	}

	// Every created entity is in the manifest, under its own tenant.
	items, err := q.ListDemoWorkspaceItems(ctx, tenantA.ID)
	if err != nil {
		t.Fatalf("list manifest: %v", err)
	}
	kinds := map[string]int{}
	for _, it := range items {
		kinds[it.EntityType]++
	}
	for kind, want := range map[string]int{
		demodata.ItemProject: 4, demodata.ItemTM: 2, demodata.ItemTermBase: 1, demodata.ItemQAProfile: 1,
		demodata.ItemCustomer: 3, demodata.ItemLabelDefinition: 1,
	} {
		if kinds[kind] != want {
			t.Errorf("manifest has %d %s rows, want %d (all: %v)", kinds[kind], kind, want, kinds)
		}
	}

	// --- Idempotent: the second call writes nothing ---
	afterFirst := countTenant(t, pool, tenantA.ID)
	again, err := demodata.Create(ctx, pool, tenantA.ID, ownerA.ID, demodata.Options{})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if !again.AlreadyPresent || len(again.Projects) != 0 {
		t.Errorf("second Create: want AlreadyPresent with no projects, got %+v", again)
	}
	assertSameCounts(t, "second call", afterFirst, countTenant(t, pool, tenantA.ID))

	// --- Tenant B and every user are untouched ---
	assertSameCounts(t, "tenant B", beforeB, countTenant(t, pool, tenantB.ID))
	namesAfter := userNames(t, pool)
	for id, name := range namesBefore {
		if namesAfter[id] != name {
			t.Errorf("user %d renamed from %q to %q", id, name, namesAfter[id])
		}
	}

	// The same dataset still fits in tenant B — the names are not global.
	resB, err := demodata.Create(ctx, pool, tenantB.ID, ownerB.ID, demodata.Options{})
	if err != nil || resB.AlreadyPresent {
		t.Fatalf("Create in tenant B after tenant A: res=%+v err=%v", resB, err)
	}
}

// No linguist in the tenant: every step goes to the owner. One provider
// connection: that engine is built, the other is skipped. OmitDemoLabel (the
// screenshot dataset) leaves the label off but still fills the manifest.
func TestIntegrationCreateDemoWorkspaceOwnerOnly(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "Solo Tenant")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "solo@test.com", "pass", "owner")
	testutil.SeedAIVendorToken(t, q, tenant.ID, "claude", "claude key", "placeholder")

	res, err := demodata.Create(ctx, pool, tenant.ID, owner.ID, demodata.Options{OmitDemoLabel: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defs, err := q.ListLabelDefinitions(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	for _, d := range defs {
		if d.Name == demodata.DemoLabelName {
			t.Error("OmitDemoLabel still created the Demo label")
		}
	}
	items, err := q.ListDemoWorkspaceItems(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("list manifest: %v", err)
	}
	projects := 0
	for _, it := range items {
		if it.EntityType == demodata.ItemProject {
			projects++
		}
	}
	if projects != 4 {
		t.Errorf("manifest lists %d projects without the label, want 4", projects)
	}
	if again, err := demodata.Create(ctx, pool, tenant.ID, owner.ID, demodata.Options{}); err != nil || !again.AlreadyPresent {
		t.Errorf("second Create without the label: want AlreadyPresent, got %+v err=%v", again, err)
	}
	if res.LinguistUserID != owner.ID {
		t.Errorf("linguist = %d, want the owner %d", res.LinguistUserID, owner.ID)
	}
	var others int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM job_step_assignments a
		JOIN jobs j ON j.id = a.job_id JOIN files f ON f.id = j.file_id JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1 AND a.assigned_to IS DISTINCT FROM $2`, tenant.ID, owner.ID).Scan(&others); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if others != 0 {
		t.Errorf("%d step assignments are not on the owner", others)
	}
	if countSkipped(res, "no linguist") != 1 {
		t.Errorf("Skipped does not mention the missing linguist: %v", res.Skipped)
	}
	if len(res.AIEngines) != 1 || countSkipped(res, "OpenAI") != 1 {
		t.Errorf("want the Claude engine built and OpenAI skipped; engines=%+v skipped=%v", res.AIEngines, res.Skipped)
	}
}

// An owner id from another tenant is refused before anything is written.
func TestIntegrationCreateDemoWorkspaceForeignOwner(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenantA := testutil.SeedTenant(t, q, "Owner Tenant A")
	tenantB := testutil.SeedTenant(t, q, "Owner Tenant B")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "owner-b@test.com", "pass", "owner")
	before := countTenant(t, pool, tenantA.ID)

	_, err := demodata.Create(ctx, pool, tenantA.ID, ownerB.ID, demodata.Options{})
	if !errors.Is(err, demodata.ErrOwnerNotMember) {
		t.Fatalf("want ErrOwnerNotMember, got %v", err)
	}
	assertSameCounts(t, "tenant A", before, countTenant(t, pool, tenantA.ID))
}

func countSkipped(res demodata.Result, substr string) int {
	n := 0
	for _, s := range res.Skipped {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

// An untranslated demo segment is NULL, as an uploaded one is — not an empty
// string. Pre-translation (Apply TM/MT) picks rows by active_text IS NULL, so
// an empty string would leave every untranslated demo segment unfillable.
func TestIntegrationCreateDemoWorkspace_UntranslatedIsNull(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "Demo Untranslated")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "demo-u@test.com", "pass", "owner")
	if _, err := demodata.Create(ctx, pool, tenant.ID, owner.ID, demodata.Options{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var empty, null int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE t.active_text = ''), count(*) FILTER (WHERE t.active_text IS NULL)
		FROM translations t JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id JOIN projects p ON p.id = f.project_id
		WHERE p.tenant_id = $1`, tenant.ID).Scan(&empty, &null); err != nil {
		t.Fatal(err)
	}
	if empty != 0 {
		t.Errorf("%d demo translations hold an empty string instead of NULL", empty)
	}
	if null == 0 {
		t.Error("the dataset has no untranslated segment, so this test checks nothing")
	}
}
