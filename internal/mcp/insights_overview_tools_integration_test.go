package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5/pgtype"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func chartArgs(title, measure string) map[string]any {
	return map[string]any{
		"title": title, "measure": measure, "dimensions": []string{"day"},
		"chart_type": "line", "x_key": "day", "series": []string{"words"},
	}
}

func TestIntegrationMCPInsightsDashboardCharts(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	create := chartArgs("Words per day", "words_processed")
	create["dashboard"] = "Ops"
	create["last_days"] = 30
	create["shared"] = true
	var created struct {
		ChartID     int64 `json:"chart_id"`
		DashboardID int64 `json:"dashboard_id"`
	}
	if isErr, text := callTool(t, owner, "openloc_create_chart", create, &created); isErr {
		t.Fatalf("create_chart: %s", text)
	}

	var list struct {
		Dashboards []struct {
			ID         int64 `json:"id"`
			ChartCount int64 `json:"chart_count"`
		} `json:"dashboards"`
	}
	if isErr, text := callTool(t, owner, "openloc_list_dashboards", map[string]any{}, &list); isErr || len(list.Dashboards) != 1 || list.Dashboards[0].ChartCount != 1 {
		t.Fatalf("list_dashboards: %v %s %+v", isErr, text, list)
	}

	type chart struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Measure string `json:"measure"`
	}
	getCharts := func() []chart {
		var d struct {
			Charts []chart `json:"charts"`
		}
		if isErr, text := callTool(t, owner, "openloc_get_dashboard", map[string]any{"dashboard_id": created.DashboardID}, &d); isErr {
			t.Fatalf("get_dashboard: %s", text)
		}
		return d.Charts
	}
	if c := getCharts(); len(c) != 1 || c[0].Measure != "words_processed" {
		t.Fatalf("charts = %+v", c)
	}

	// Update: a bad measure fails at preview; a good one previews the diff,
	// changes nothing, then saves on confirm.
	bad := chartArgs("Words per day", "no_such_measure")
	bad["chart_id"] = created.ChartID
	if isErr, text := callTool(t, owner, "openloc_update_chart", bad, nil); !isErr || !strings.Contains(text, "not a value this measure returns") {
		t.Fatalf("bad measure accepted: %v %s", isErr, text)
	}
	upd := chartArgs("QA issues per day", "qa_issues")
	upd["chart_id"] = created.ChartID
	upd["series"] = []string{"issues"}
	var g guardedOut
	if isErr, text := callTool(t, owner, "openloc_update_chart", upd, &g); isErr {
		t.Fatalf("preview update: %s", text)
	}
	if !strings.Contains(g.Preview.Summary, "measure words_processed → qa_issues") {
		t.Errorf("preview summary = %q", g.Preview.Summary)
	}
	if c := getCharts(); c[0].Title != "Words per day" {
		t.Fatalf("preview changed the chart: %+v", c)
	}
	if isErr, text := confirmedCall(t, owner, "openloc_update_chart", upd, nil); isErr {
		t.Fatalf("confirm update: %s", text)
	}
	if c := getCharts(); c[0].Title != "QA issues per day" || c[0].Measure != "qa_issues" {
		t.Errorf("chart after update = %+v", c)
	}

	// A shared dashboard is owner/admin to change, like the REST routes
	// (dashboard.MayWrite): a manager can read it but not edit or delete.
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-dash@test.com", "pass", "project_manager")
	pmSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	if isErr, text := callTool(t, pmSession, "openloc_get_dashboard", map[string]any{"dashboard_id": created.DashboardID}, nil); isErr {
		t.Fatalf("pm cannot read a shared dashboard: %s", text)
	}
	for _, tool := range []string{"openloc_delete_chart", "openloc_update_chart"} {
		args := map[string]any{"chart_id": created.ChartID}
		if tool == "openloc_update_chart" {
			args = upd
		}
		if isErr, text := callTool(t, pmSession, tool, args, nil); !isErr || !strings.Contains(text, "only an owner or admin") {
			t.Fatalf("pm changed a shared chart via %s: %v %s", tool, isErr, text)
		}
	}

	// A personal dashboard is its owner's alone: the manager edits their own,
	// and the org owner cannot even see it.
	mine := chartArgs("My words", "words_processed")
	mine["dashboard"] = "PM board"
	mine["last_days"] = 30
	var pmChart struct {
		ChartID     int64 `json:"chart_id"`
		DashboardID int64 `json:"dashboard_id"`
	}
	if isErr, text := callTool(t, pmSession, "openloc_create_chart", mine, &pmChart); isErr {
		t.Fatalf("pm create_chart: %s", text)
	}
	pmUpd := chartArgs("My words (edited)", "words_processed")
	pmUpd["chart_id"] = pmChart.ChartID
	if isErr, text := confirmedCall(t, pmSession, "openloc_update_chart", pmUpd, nil); isErr {
		t.Fatalf("pm update of own chart: %s", text)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_get_dashboard", map[string]any{"dashboard_id": pmChart.DashboardID}},
		{"openloc_update_chart", pmUpd},
		{"openloc_delete_chart", map[string]any{"chart_id": pmChart.ChartID}},
	} {
		isErr, text := callTool(t, owner, c.tool, c.args, nil)
		if !isErr {
			t.Fatalf("the org owner reached a manager's personal chart via %s", c.tool)
		}
		assertOpaqueNotFound(t, text)
	}
	if isErr, text := confirmedCall(t, pmSession, "openloc_delete_chart", map[string]any{"chart_id": pmChart.ChartID}, nil); isErr {
		t.Fatalf("pm delete of own chart: %s", text)
	}

	// Cross-tenant: B sees nothing of A's dashboards.
	sB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_get_dashboard", map[string]any{"dashboard_id": created.DashboardID}},
		{"openloc_delete_chart", map[string]any{"chart_id": created.ChartID}},
		{"openloc_update_chart", upd},
	} {
		isErr, text := callTool(t, sB, c.tool, c.args, nil)
		if !isErr {
			t.Fatalf("tenant B reached A's chart via %s", c.tool)
		}
		assertOpaqueNotFound(t, text)
	}

	if isErr, text := confirmedCall(t, owner, "openloc_delete_chart", map[string]any{"chart_id": created.ChartID}, nil); isErr {
		t.Fatalf("delete: %s", text)
	}
	var deleted bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM dashboard_charts WHERE id = $1`, created.ChartID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("chart row after delete: deleted=%v err=%v", deleted, err)
	}
	if c := getCharts(); len(c) != 0 {
		t.Errorf("deleted chart still on the dashboard: %+v", c)
	}
	rec := testutil.DoRequest(t, testutil.NewTestRouter(f.pool), http.MethodGet,
		"/api/dashboards/charts/"+itoa(created.ChartID)+"/data", nil, f.ownerA.ID, f.tenantA.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("REST data of a deleted chart = %d, want 404", rec.Code)
	}
}

// seedJob adds a one-segment job to a project and returns it.
func seedJob(t *testing.T, f *fixture, projectID int64, name, lang string) db.Job {
	t.Helper()
	return seedJobWithSegments(t, f.q, projectID, name, lang, []string{"Segment of " + name})
}

func TestIntegrationMCPInsightsAttentionSummary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	overdueStep := testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	if err := f.q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID: overdueStep, Deadline: pgtype.Timestamptz{Time: time.Now().Add(-48 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	unassigned := seedJob(t, f, f.projectA.ID, "unassigned.txt", "de")
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, unassigned.ID, nil)

	stuck := seedJob(t, f, f.projectA.ID, "stuck.txt", "de")
	execSQL(t, f, `UPDATE jobs SET updated_at = now() - interval '10 days' WHERE id = $1`, stuck.ID)
	execSQL(t, f, `UPDATE translations SET updated_at = now() - interval '10 days'
		WHERE segment_id IN (SELECT id FROM segments WHERE file_id = $1)`, stuck.FileID)

	failed := seedJob(t, f, f.projectA.ID, "failed.txt", "de")
	execSQL(t, f, `UPDATE jobs SET status = 'failed', error_message = 'parser crashed' WHERE id = $1`, failed.ID)

	hot := seedJob(t, f, f.projectA.ID, "hot.txt", "de")
	execSQL(t, f, `INSERT INTO qa_results (translation_id, check_type, severity, message, ignored)
		SELECT t.id, c.check_type, c.severity, 'x', c.ignored
		FROM translations t JOIN segments s ON s.id = t.segment_id,
		     (VALUES ('number', 'error', false), ('tags', 'error', false),
		             ('spelling', 'error', true), ('punctuation', 'warning', false)) AS c(check_type, severity, ignored)
		WHERE s.file_id = $1`, hot.FileID)

	// Noise that must not show: another tenant's failed job, and a failed job
	// in an on-hold project.
	execSQL(t, f, `UPDATE jobs SET status = 'failed', error_message = 'other tenant' WHERE id = $1`, f.jobB.ID)
	onHold := testutil.SeedProject(t, f.q, f.tenantA.ID, "Paused", "en", []string{"de"})
	execSQL(t, f, `UPDATE projects SET status = 'on_hold' WHERE id = $1`, onHold.ID)
	paused := seedJob(t, f, onHold.ID, "paused.txt", "de")
	execSQL(t, f, `UPDATE jobs SET status = 'failed' WHERE id = $1`, paused.ID)

	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-att@test.com", "pass", "project_manager")
	s := connect(t, f.url, testutil.SeedAPIToken(t, f.q, f.tenantA.ID, pm.ID, auth.CapabilityRead, time.Hour))

	type item struct {
		JobID     int64  `json:"job_id"`
		ProjectID int64  `json:"project_id"`
		Detail    string `json:"detail"`
	}
	var out struct {
		Overdue    []item `json:"overdue"`
		Unassigned []item `json:"unassigned"`
		Stuck      []item `json:"stuck"`
		Failed     []item `json:"failed"`
		QAHotspots []item `json:"qa_hotspots"`
		StaleDays  int    `json:"stale_days"`
	}
	if isErr, text := callTool(t, s, "openloc_get_attention_summary", map[string]any{}, &out); isErr {
		t.Fatalf("attention: %s", text)
	}
	ids := func(items []item) []int64 {
		r := []int64{}
		for _, i := range items {
			r = append(r, i.JobID)
		}
		return r
	}
	expect := func(name string, items []item, want int64) {
		t.Helper()
		if len(items) != 1 || items[0].JobID != want || items[0].ProjectID != f.projectA.ID {
			t.Errorf("%s = %v, want exactly job %d", name, ids(items), want)
		}
	}
	expect("overdue", out.Overdue, f.jobA.ID)
	expect("unassigned", out.Unassigned, unassigned.ID)
	expect("stuck", out.Stuck, stuck.ID)
	expect("failed", out.Failed, failed.ID)
	expect("qa_hotspots", out.QAHotspots, hot.ID)
	if out.StaleDays != 3 {
		t.Errorf("default stale_days = %d, want 3", out.StaleDays)
	}
	if len(out.QAHotspots) == 1 && !strings.HasPrefix(out.QAHotspots[0].Detail, "2 unresolved QA errors") {
		t.Errorf("hotspot must count only unignored errors: %q", out.QAHotspots[0].Detail)
	}
	if len(out.Failed) == 1 && !strings.Contains(out.Failed[0].Detail, "parser crashed") {
		t.Errorf("failed detail = %q", out.Failed[0].Detail)
	}

	// stale_days widens or narrows the stuck list.
	if isErr, text := callTool(t, s, "openloc_get_attention_summary", map[string]any{"stale_days": 30}, &out); isErr || len(out.Stuck) != 0 {
		t.Errorf("stale_days=30 still lists stuck jobs: %v %s %v", isErr, text, ids(out.Stuck))
	}
}

func TestIntegrationMCPInsightsProgressBuckets(t *testing.T) {
	f := newFixture(t)
	// Job A: 3 segments. Plus a second file with 2 more — five segments, one
	// per bucket, 10 words each.
	extra := seedJobWithSegments(t, f.q, f.projectA.ID, "extra.txt", "de", []string{"Four", "Five"})
	execSQL(t, f, `UPDATE segments SET word_count = 10 WHERE file_id IN ($1, $2)`, f.jobA.FileID, extra.FileID)
	setStatus := func(fileID int64, pos int, sql string) {
		execSQL(t, f, `UPDATE translations SET `+sql+` WHERE segment_id =
			(SELECT id FROM segments WHERE file_id = $1 AND position = $2)`, fileID, pos)
	}
	setStatus(f.jobA.FileID, 0, `status = 'approved', active_text = 'Hallo Welt'`)
	setStatus(f.jobA.FileID, 1, `active_text = 'MT text'`)
	// jobA position 2 stays untranslated.
	setStatus(extra.FileID, 0, `active_text = 'edited', edited_by = `+itoa(f.ownerA.ID))
	setStatus(extra.FileID, 1, `status = 'auto_approved', active_text = 'QP ok'`)

	newer := testutil.SeedProject(t, f.q, f.tenantA.ID, "Newer", "en", []string{"fr"})
	seedJobWithSegments(t, f.q, newer.ID, "newer.txt", "fr", []string{"Bonjour"})

	s := connect(t, f.url, f.tokenA)
	type counts struct {
		Segments int64 `json:"segments"`
		Words    int64 `json:"words"`
	}
	var page struct {
		Rows []struct {
			ProjectID        int64   `json:"project_id"`
			TargetLanguage   string  `json:"target_language"`
			Untranslated     counts  `json:"untranslated"`
			PreTranslated    counts  `json:"pre_translated"`
			Draft            counts  `json:"draft"`
			AutoApproved     counts  `json:"auto_approved"`
			Confirmed        counts  `json:"confirmed"`
			Total            counts  `json:"total"`
			PercentDoneWords float64 `json:"percent_done_words"`
		} `json:"rows"`
		NextCursor int64 `json:"next_cursor"`
		HasMore    bool  `json:"has_more"`
	}
	if isErr, text := callTool(t, s, "openloc_get_progress", map[string]any{"project_id": f.projectA.ID}, &page); isErr {
		t.Fatalf("progress: %s", text)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %+v", page.Rows)
	}
	r := page.Rows[0]
	one := counts{1, 10}
	if r.Untranslated != one || r.PreTranslated != one || r.Draft != one || r.AutoApproved != one || r.Confirmed != one || r.Total != (counts{5, 50}) {
		t.Errorf("buckets = %+v", r)
	}
	if r.PercentDoneWords != 40 {
		t.Errorf("percent done = %v, want 40", r.PercentDoneWords)
	}

	// Paging by project, newest first; tenant B's project never appears.
	if isErr, text := callTool(t, s, "openloc_get_progress", map[string]any{"limit": 1}, &page); isErr {
		t.Fatalf("progress page 1: %s", text)
	}
	if len(page.Rows) != 1 || page.Rows[0].ProjectID != newer.ID || !page.HasMore || page.NextCursor != newer.ID {
		t.Fatalf("page 1 = %+v", page)
	}
	if isErr, text := callTool(t, s, "openloc_get_progress", map[string]any{"limit": 1, "cursor": page.NextCursor}, &page); isErr {
		t.Fatalf("progress page 2: %s", text)
	}
	if len(page.Rows) != 1 || page.Rows[0].ProjectID != f.projectA.ID {
		t.Fatalf("page 2 = %+v", page)
	}
	if isErr, text := callTool(t, s, "openloc_get_progress", map[string]any{"project_id": f.projectB.ID}, nil); !isErr {
		t.Fatal("progress of tenant B's project returned")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPInsightsNotificationsAreTheCallersOwn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	notify := func(userID int64, subject string) db.Notification {
		n, err := f.q.CreateNotification(ctx, db.CreateNotificationParams{
			TenantID: f.tenantA.ID, UserID: userID, EventType: "assignment", Subject: subject,
			ReferenceType: "job", ReferenceID: f.jobA.ID,
		})
		if err != nil {
			t.Fatalf("seed notification: %v", err)
		}
		return n
	}
	mine1 := notify(f.linguistA.ID, "Job assigned")
	notify(f.linguistA.ID, "Deadline tomorrow")
	ownerN := notify(f.ownerA.ID, "Owner only")

	type list struct {
		Notifications []struct {
			ID      int64  `json:"id"`
			Subject string `json:"subject"`
		} `json:"notifications"`
		UnreadCount int64 `json:"unread_count"`
	}
	var l list
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_list_notifications", map[string]any{}, &l); isErr {
		t.Fatalf("list: %s", text)
	}
	if len(l.Notifications) != 2 || l.UnreadCount != 2 {
		t.Fatalf("linguist sees %+v", l)
	}
	for _, n := range l.Notifications {
		if n.ID == ownerN.ID {
			t.Fatal("linguist sees the owner's notification")
		}
	}

	// Read-only token cannot mark.
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_mark_notifications_read", map[string]any{"all": true}, nil); !isErr || !strings.Contains(text, "write access") {
		t.Fatalf("read-only mark: %v %s", isErr, text)
	}

	w := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	var marked struct {
		UnreadCount int64 `json:"unread_count"`
	}
	if isErr, text := callTool(t, w, "openloc_mark_notifications_read", map[string]any{"ids": []int64{mine1.ID, ownerN.ID}}, &marked); isErr {
		t.Fatalf("mark: %s", text)
	}
	if marked.UnreadCount != 1 {
		t.Errorf("unread after marking one = %d, want 1", marked.UnreadCount)
	}
	ownerUnread, err := f.q.CountUnreadNotifications(ctx, db.CountUnreadNotificationsParams{UserID: f.ownerA.ID, TenantID: f.tenantA.ID})
	if err != nil || ownerUnread != 1 {
		t.Errorf("the linguist marked the owner's notification read: unread=%d err=%v", ownerUnread, err)
	}
	if isErr, text := callTool(t, w, "openloc_mark_notifications_read", map[string]any{"all": true}, &marked); isErr || marked.UnreadCount != 0 {
		t.Errorf("mark all: %v %s %+v", isErr, text, marked)
	}

	// Preferences: a partial update keeps the other switches.
	var prefs map[string]bool
	if isErr, text := callTool(t, w, "openloc_update_notification_preferences", map[string]any{"email_deadline": false}, &prefs); isErr {
		t.Fatalf("update prefs: %s", text)
	}
	stored, err := f.q.GetNotificationPreferences(ctx, f.linguistA.ID)
	if err != nil || stored.EmailDeadline || !stored.EmailAssignment || !stored.EmailRejection || !stored.EmailCompletion {
		t.Errorf("stored prefs = %+v err %v", stored, err)
	}
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_get_notification_preferences", map[string]any{}, &prefs); isErr || prefs["email_deadline"] || !prefs["email_assignment"] {
		t.Errorf("get prefs: %v %s %v", isErr, text, prefs)
	}
}

// Every insight read result must satisfy its advertised output schema; a nil
// slice serialized as null is the usual way to break that.
func TestIntegrationMCPInsightsResultsMatchSchemas(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	listed, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	schemas := map[string]*jsonschema.Resolved{}
	for _, tool := range listed.Tools {
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal schema: %v", err)
		}
		var sch jsonschema.Schema
		if err := json.Unmarshal(raw, &sch); err != nil {
			t.Fatalf("parse schema for %s: %v", tool.Name, err)
		}
		resolved, err := sch.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve schema for %s: %v", tool.Name, err)
		}
		schemas[tool.Name] = resolved
	}
	calls := map[string]map[string]any{
		"openloc_get_project_analysis":         {"project_id": f.projectA.ID},
		"openloc_list_saved_analyses":          {"project_id": f.projectA.ID},
		"openloc_get_productivity":             {"scope": "project_by_step", "project_id": f.projectA.ID},
		"openloc_list_dashboards":              {},
		"openloc_get_me":                       {},
		"openloc_list_invites":                 {},
		"openloc_list_notifications":           {},
		"openloc_get_notification_preferences": {},
		"openloc_get_attention_summary":        {},
		"openloc_get_progress":                 {},
	}
	for tool, args := range calls {
		t.Run(tool, func(t *testing.T) {
			res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
			if err != nil || res.IsError {
				t.Fatalf("call: %v %+v", err, res)
			}
			raw, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var generic any
			if err := json.Unmarshal(raw, &generic); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := schemas[tool].Validate(generic); err != nil {
				t.Errorf("result does not satisfy the advertised schema: %v\npayload: %s", err, raw)
			}
		})
	}
}
