package mcp_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// execSQL runs fixture set-up SQL that has no sqlc query (timestamps, counts).
func execSQL(t *testing.T, f *fixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seedWeightedJobA gives job A's three segments 10 words each, makes the first
// an exact TM match and sets the org's "new" band weight to 50%. Expected:
// 10 exact words × 0% + 20 new words × 50% = 10 weighted words.
func seedWeightedJobA(t *testing.T, f *fixture) {
	t.Helper()
	execSQL(t, f, `UPDATE segments SET word_count = 10 WHERE file_id = $1`, f.jobA.FileID)
	execSQL(t, f, `UPDATE translations SET origin = 'tm', tm_match_percent = 100
		WHERE id = (SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		            WHERE s.file_id = $1 AND s.position = 0)`, f.jobA.FileID)
	execSQL(t, f, `INSERT INTO analysis_weights (tenant_id, band, weight_pct) VALUES ($1, 'new', 50)`, f.tenantA.ID)
}

func TestIntegrationMCPInsightsProjectAnalysisWeightsBands(t *testing.T) {
	f := newFixture(t)
	seedWeightedJobA(t, f)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-analysis@test.com", "pass", "project_manager")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))

	type band struct {
		Words         int32 `json:"words"`
		WeightPct     int32 `json:"weight_pct"`
		WeightedWords int32 `json:"weighted_words"`
	}
	var analysis struct {
		Languages map[string]struct {
			TMExact band `json:"tm_exact"`
			New     band `json:"new"`
			Total   band `json:"total"`
		} `json:"languages"`
	}
	if isErr, text := callTool(t, s, "openloc_get_project_analysis", map[string]any{"project_id": f.projectA.ID}, &analysis); isErr {
		t.Fatalf("get_project_analysis: %s", text)
	}
	de, ok := analysis.Languages["de"]
	if !ok || len(analysis.Languages) != 1 {
		t.Fatalf("want one language de, got %+v", analysis.Languages)
	}
	if de.TMExact != (band{10, 0, 0}) {
		t.Errorf("tm_exact words/weight/weighted = %+v, want {10 0 0}", de.TMExact)
	}
	if de.New != (band{20, 50, 10}) {
		t.Errorf("new words/weight/weighted = %+v, want {20 50 10}", de.New)
	}
	if de.Total.WeightedWords != 10 {
		t.Errorf("total weighted words = %d, want 10", de.Total.WeightedWords)
	}

	// Cross-tenant project: opaque not-found.
	isErr, text := callTool(t, s, "openloc_get_project_analysis", map[string]any{"project_id": f.projectB.ID}, nil)
	if !isErr {
		t.Fatal("get_project_analysis returned tenant B's project")
	}
	assertOpaqueNotFound(t, text)
}

func TestIntegrationMCPInsightsSavedAnalysisLifecycle(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var saved struct {
		ID     int64   `json:"id"`
		Status string  `json:"status"`
		JobIDs []int64 `json:"job_ids"`
	}
	if isErr, text := callTool(t, s, "openloc_save_analysis", map[string]any{
		"project_id": f.projectA.ID, "job_ids": []int64{f.jobA.ID}, "name": "Weekly check",
	}, &saved); isErr {
		t.Fatalf("save_analysis: %s", text)
	}
	row, err := f.q.GetAnalysis(context.Background(), db.GetAnalysisParams{ID: saved.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("saved analysis not in DB: %v", err)
	}
	if row.Name != "Weekly check" || len(row.JobIds) != 1 || row.JobIds[0] != f.jobA.ID || saved.Status != "pending" {
		t.Errorf("stored analysis = %+v (tool status %q)", row, saved.Status)
	}

	// A job of another tenant's project is a caller mistake, not a save.
	if isErr, text := callTool(t, s, "openloc_save_analysis", map[string]any{
		"project_id": f.projectA.ID, "job_ids": []int64{f.jobB.ID},
	}, nil); !isErr || !strings.Contains(text, "do not belong to this project") {
		t.Fatalf("foreign job accepted: %v %s", isErr, text)
	}

	var list struct {
		Count    int `json:"count"`
		Analyses []struct {
			ID int64 `json:"id"`
		} `json:"analyses"`
	}
	if isErr, text := callTool(t, s, "openloc_list_saved_analyses", map[string]any{"project_id": f.projectA.ID}, &list); isErr || list.Count != 1 {
		t.Fatalf("list_saved_analyses: %v %s %+v", isErr, text, list)
	}
	byID := map[string]any{"project_id": f.projectA.ID, "analysis_id": saved.ID}
	if isErr, text := callTool(t, s, "openloc_list_saved_analyses", byID, &list); isErr || list.Count != 1 || list.Analyses[0].ID != saved.ID {
		t.Fatalf("list_saved_analyses by id: %v %s %+v", isErr, text, list)
	}

	// Tenant B gets not-found, not an empty list, for the same calls, and
	// cannot reach the delete preview while the analysis is live.
	sB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_list_saved_analyses", map[string]any{"project_id": f.projectA.ID}},
		{"openloc_list_saved_analyses", byID},
		{"openloc_delete_saved_analysis", map[string]any{"analysis_id": saved.ID}},
	} {
		isErr, text := callTool(t, sB, c.tool, c.args, nil)
		if !isErr {
			t.Errorf("%s %v: tenant B reached tenant A's analysis", c.tool, c.args)
			continue
		}
		assertOpaqueNotFound(t, text)
	}

	// Preview changes nothing and names the effect.
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_delete_saved_analysis", map[string]any{"analysis_id": saved.ID}, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Status != "needs_confirmation" || !strings.Contains(g.Preview.Summary, "Weekly check") {
		t.Fatalf("preview = %+v", g)
	}
	if _, err := f.q.GetAnalysis(context.Background(), db.GetAnalysisParams{ID: saved.ID, TenantID: f.tenantA.ID}); err != nil {
		t.Fatalf("preview deleted the analysis: %v", err)
	}

	if isErr, text := confirmedCall(t, s, "openloc_delete_saved_analysis", map[string]any{"analysis_id": saved.ID}, nil); isErr {
		t.Fatalf("confirm delete: %s", text)
	}
	// Soft delete: the row stays with deleted_at, and every read hides it.
	var deleted bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM analyses WHERE id = $1`, saved.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("analysis row after delete: deleted=%v err=%v", deleted, err)
	}
	if isErr, _ := callTool(t, s, "openloc_list_saved_analyses", map[string]any{"project_id": f.projectA.ID}, &list); isErr || list.Count != 0 {
		t.Errorf("deleted analysis still listed: %+v", list)
	}
	rec := testutil.DoRequest(t, testutil.NewTestRouter(f.pool), http.MethodGet, "/api/analyses/"+itoa(saved.ID), nil, f.ownerA.ID, f.tenantA.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("REST GET of a deleted analysis = %d, want 404", rec.Code)
	}
}

func TestIntegrationMCPInsightsProductivity(t *testing.T) {
	f := newFixture(t)
	seedWeightedJobA(t, f)
	execSQL(t, f, `UPDATE translations SET edited_by = $1, editing_time_ms = 60000, status = 'approved'
		WHERE segment_id IN (SELECT id FROM segments WHERE file_id = $2)`, f.linguistA.ID, f.jobA.FileID)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-prod@test.com", "pass", "project_manager")
	pmSession := connect(t, f.url, testutil.SeedAPIToken(t, f.q, f.tenantA.ID, pm.ID, auth.CapabilityRead, time.Hour))

	var report struct {
		Rows []struct {
			UserID      *int64 `json:"user_id"`
			WordsEdited int32  `json:"words_edited"`
		} `json:"rows"`
	}
	args := map[string]any{"scope": "project", "project_id": f.projectA.ID}
	if isErr, text := callTool(t, pmSession, "openloc_get_productivity", args, &report); isErr {
		t.Fatalf("pm productivity: %s", text)
	}
	if len(report.Rows) != 1 || report.Rows[0].WordsEdited != 30 || report.Rows[0].UserID == nil || *report.Rows[0].UserID != f.linguistA.ID {
		t.Fatalf("rows = %+v", report.Rows)
	}

	// Foreign project and bad scope.
	if isErr, text := callTool(t, pmSession, "openloc_get_productivity", map[string]any{"scope": "project", "project_id": f.projectB.ID}, nil); !isErr {
		t.Error("productivity of tenant B's project returned")
	} else {
		assertOpaqueNotFound(t, text)
	}
	if isErr, text := callTool(t, pmSession, "openloc_get_productivity", map[string]any{"scope": "galaxy"}, nil); !isErr || !strings.Contains(text, "scope must be") {
		t.Errorf("bad scope: %v %s", isErr, text)
	}
}

func TestIntegrationMCPInsightsAnalysisToolsHiddenFromLinguists(t *testing.T) {
	f := newFixture(t)
	names := listToolNames(t, connect(t, f.url, f.tokenLingA))
	for _, hidden := range []string{
		"openloc_get_project_analysis", "openloc_save_analysis",
		"openloc_get_productivity", "openloc_get_attention_summary", "openloc_get_progress",
		"openloc_list_invites", "openloc_change_member_role", "openloc_list_dashboards",
	} {
		for _, n := range names {
			if n == hidden {
				t.Errorf("linguist sees manager tool %s", hidden)
			}
		}
	}
}
