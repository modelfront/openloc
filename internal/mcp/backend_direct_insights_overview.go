package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	"openloc/internal/dashboard"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/sliceutil"
)

// --- Dashboards -------------------------------------------------------------

func dashboardChartView(c dashboard.Chart) DashboardChart {
	return DashboardChart{
		ID: c.ID, DashboardID: c.DashboardID, Title: c.Title, Measure: c.Definition.Measure,
		Dimensions: sliceutil.NonNil(c.Definition.Dimensions), Filters: nonNilMap(c.Definition.Filters),
		ChartType: c.Viz.Type, XKey: c.Viz.XKey, Series: sliceutil.NonNil(c.Viz.Series), SeriesKey: c.Viz.SeriesKey,
		Controls: sliceutil.NonNil(c.Controls), Position: c.Position,
	}
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// viewer is the caller as the dashboard owner rules see them.
func viewer(ctx context.Context) dashboard.Viewer {
	tenantID, userID := scope(ctx)
	return dashboard.Viewer{TenantID: tenantID, UserID: userID, Role: auth.RoleFromContext(ctx)}
}

// dashboardErr maps the dashboard service's errors: a write on a dashboard
// the caller may only read is denied, a definition the metrics compiler
// rejects is invalid.
func dashboardErr(err error) error {
	var ue dashboard.UnprocessableError
	switch {
	case errors.Is(err, dashboard.ErrReadOnly):
		return fmt.Errorf("%w: %s", errDenied, err.Error())
	case errors.As(err, &ue):
		return invalidf("%s", ue.Msg)
	}
	return serviceErr(err, "chart")
}

func dashboardWho(personal bool) string {
	if personal {
		return "It is on the caller's own dashboard; nobody else sees it."
	}
	return "The dashboard is shared by the whole organization; everyone sees the change."
}

func (b *directBackend) ListDashboards(ctx context.Context, _ ListDashboardsIn) (DashboardList, error) {
	rows, err := dashboard.List(ctx, b.q, viewer(ctx))
	if err != nil {
		return DashboardList{}, err
	}
	out := make([]DashboardSummary, 0, len(rows))
	for _, d := range rows {
		out = append(out, DashboardSummary{ID: d.ID, Name: d.Name, Personal: d.Personal, ChartCount: d.ChartCount, CreatedAt: timeString(d.CreatedAt)})
	}
	return DashboardList{Dashboards: out, Count: len(out)}, nil
}

func (b *directBackend) GetDashboard(ctx context.Context, in GetDashboardIn) (DashboardDetail, error) {
	d, err := dashboard.Get(ctx, b.q, viewer(ctx), in.DashboardID)
	if err != nil {
		return DashboardDetail{}, dashboardErr(err)
	}
	charts := make([]DashboardChart, 0, len(d.Charts))
	for _, c := range d.Charts {
		charts = append(charts, dashboardChartView(c))
	}
	return DashboardDetail{ID: d.ID, Name: d.Name, Personal: d.Personal, CanEdit: d.CanEdit, CreatedAt: timeString(d.CreatedAt), Charts: charts}, nil
}

func (in UpdateChartIn) spec(current []string) dashboard.ChartSpec {
	controls := in.Controls
	if controls == nil {
		controls = current
	}
	return dashboard.ChartSpec{
		Title: in.Title, Measure: in.Measure, Dimensions: in.Dimensions, ChartType: in.ChartType,
		XKey: in.XKey, Series: in.Series, SeriesKey: in.SeriesKey, Controls: controls,
	}
}

// chartSpecFor reads the chart's current controls, so an update that leaves
// controls out keeps them.
func (b *directBackend) chartSpecFor(ctx context.Context, in UpdateChartIn) (dashboard.ChartSpec, error) {
	if in.Controls != nil {
		return in.spec(nil), nil
	}
	c, err := dashboard.GetChart(ctx, b.q, viewer(ctx), in.ChartID)
	if err != nil {
		return dashboard.ChartSpec{}, dashboardErr(err)
	}
	return in.spec(c.Controls), nil
}

func (b *directBackend) PreviewUpdateChart(ctx context.Context, in UpdateChartIn) (Preview, error) {
	spec, err := b.chartSpecFor(ctx, in)
	if err != nil {
		return Preview{}, err
	}
	v := viewer(ctx)
	old, _, err := dashboard.PlanUpdateChart(ctx, b.q, v, in.ChartID, spec)
	if err != nil {
		return Preview{}, dashboardErr(err)
	}
	d, err := dashboard.Get(ctx, b.q, v, old.DashboardID)
	if err != nil {
		return Preview{}, dashboardErr(err)
	}
	var changes []string
	if old.Title != strings.TrimSpace(in.Title) {
		changes = append(changes, fmt.Sprintf("title “%s” → “%s”", old.Title, strings.TrimSpace(in.Title)))
	}
	if old.Definition.Measure != in.Measure {
		changes = append(changes, fmt.Sprintf("measure %s → %s", old.Definition.Measure, in.Measure))
	}
	if strings.Join(old.Definition.Dimensions, ",") != strings.Join(in.Dimensions, ",") {
		changes = append(changes, fmt.Sprintf("dimensions [%s] → [%s]", strings.Join(old.Definition.Dimensions, ", "), strings.Join(in.Dimensions, ", ")))
	}
	if old.Viz.Type != in.ChartType || old.Viz.XKey != in.XKey || old.Viz.SeriesKey != in.SeriesKey ||
		strings.Join(old.Viz.Series, ",") != strings.Join(in.Series, ",") {
		changes = append(changes, fmt.Sprintf("drawn as %s of [%s] by %s", in.ChartType, strings.Join(in.Series, ", "), in.XKey))
	}
	if strings.Join(old.Controls, ",") != strings.Join(spec.Controls, ",") {
		changes = append(changes, fmt.Sprintf("controls [%s] → [%s]", strings.Join(old.Controls, ", "), strings.Join(spec.Controls, ", ")))
	}
	if len(changes) == 0 {
		changes = []string{"nothing changes"}
	}
	return Preview{
		Summary: fmt.Sprintf("Change the chart “%s”: %s.", old.Title, strings.Join(changes, "; ")),
		Effects: []string{dashboardWho(d.Personal)},
	}, nil
}

func (b *directBackend) UpdateChart(ctx context.Context, in UpdateChartIn) (DashboardChart, error) {
	spec, err := b.chartSpecFor(ctx, in)
	if err != nil {
		return DashboardChart{}, err
	}
	c, err := dashboard.UpdateChart(ctx, b.q, viewer(ctx), in.ChartID, spec)
	if err != nil {
		return DashboardChart{}, dashboardErr(err)
	}
	return dashboardChartView(c), nil
}

func (b *directBackend) PreviewDeleteChart(ctx context.Context, in DeleteChartIn) (Preview, error) {
	v := viewer(ctx)
	c, err := dashboard.PreviewDeleteChart(ctx, b.q, v, in.ChartID)
	if err != nil {
		return Preview{}, dashboardErr(err)
	}
	d, err := dashboard.Get(ctx, b.q, v, c.DashboardID)
	if err != nil {
		return Preview{}, dashboardErr(err)
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the chart “%s” (%s by %s) from “%s”.", c.Title, c.Definition.Measure, strings.Join(c.Definition.Dimensions, ", "), d.Name),
		Effects: []string{dashboardWho(d.Personal), "Its share link stops working. The underlying data is not touched."},
	}, nil
}

func (b *directBackend) DeleteChart(ctx context.Context, in DeleteChartIn) (ChartDeleted, error) {
	if err := dashboard.DeleteChart(ctx, b.q, viewer(ctx), in.ChartID); err != nil {
		return ChartDeleted{}, dashboardErr(err)
	}
	return ChartDeleted{ChartID: in.ChartID, Deleted: true}, nil
}

// --- Attention --------------------------------------------------------------

const (
	defaultStaleDays      = 3
	maxStaleDays          = 90
	defaultAttentionLimit = 10
	maxAttentionLimit     = 50
)

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func tsDetail(label string, ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return label + ": none"
	}
	return label + ": " + ts.Time.UTC().Format(time.RFC3339)
}

// GetAttentionSummary lists what a project manager should look at now. Every
// list is tenant-scoped and capped; the lists other than overdue look at
// in_progress projects only.
func (b *directBackend) GetAttentionSummary(ctx context.Context, in GetAttentionSummaryIn) (AttentionSummary, error) {
	tenantID, _ := scope(ctx)
	staleDays := clampInt(in.StaleDays, defaultStaleDays, maxStaleDays)
	limit := clampInt(in.Limit, defaultAttentionLimit, maxAttentionLimit)
	now := time.Now()
	out := AttentionSummary{
		StaleDays: staleDays, LimitPerList: limit, GeneratedAt: timeString(now),
		Definition: fmt.Sprintf("overdue: current step deadline passed, job not completed (any project). "+
			"unassigned: current human or hybrid step has no assignee. "+
			"stuck: open job with no job update and no translation edit for %d days. "+
			"failed: pipeline ended in an error. "+
			"qa_hotspots: open jobs ranked by unresolved (not ignored) QA errors. "+
			"All lists but overdue cover in-progress projects. Each list holds at most %d jobs.", staleDays, limit),
	}

	overdue, err := b.q.ListMCPJobs(ctx, db.ListMCPJobsParams{TenantID: tenantID, OverdueOnly: true, RowLimit: int32(limit)})
	if err != nil {
		return AttentionSummary{}, fmt.Errorf("overdue jobs: %w", err)
	}
	out.Overdue = make([]AttentionJob, 0, len(overdue))
	for _, r := range overdue {
		out.Overdue = append(out.Overdue, AttentionJob{
			JobID: r.JobID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, FileName: r.FileName,
			TargetLanguage: r.TargetLanguage, Status: r.Status, CurrentStep: r.CurrentStep,
			Detail: tsDetail("deadline", r.StepDeadline),
		})
	}

	unassigned, err := b.q.ListUnassignedCurrentSteps(ctx, db.ListUnassignedCurrentStepsParams{TenantID: tenantID, RowLimit: int32(limit)})
	if err != nil {
		return AttentionSummary{}, fmt.Errorf("unassigned steps: %w", err)
	}
	out.Unassigned = make([]AttentionJob, 0, len(unassigned))
	for _, r := range unassigned {
		out.Unassigned = append(out.Unassigned, AttentionJob{
			JobID: r.JobID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, FileName: r.FileName,
			TargetLanguage: r.TargetLanguage, CurrentStep: r.StepName,
			Detail: fmt.Sprintf("step “%s” has no assignee; %s", r.StepName, tsDetail("deadline", r.StepDeadline)),
		})
	}

	stale, err := b.q.ListStaleJobs(ctx, db.ListStaleJobsParams{
		TenantID: tenantID, Before: pgtype.Timestamptz{Time: now.AddDate(0, 0, -staleDays), Valid: true}, RowLimit: int32(limit),
	})
	if err != nil {
		return AttentionSummary{}, fmt.Errorf("stale jobs: %w", err)
	}
	out.Stuck = make([]AttentionJob, 0, len(stale))
	for _, r := range stale {
		out.Stuck = append(out.Stuck, AttentionJob{
			JobID: r.JobID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, FileName: r.FileName,
			TargetLanguage: r.TargetLanguage, Status: r.Status, CurrentStep: r.CurrentStep,
			Detail: tsDetail("last activity", r.LastActivityAt),
		})
	}

	failed, err := b.q.ListFailedJobs(ctx, db.ListFailedJobsParams{TenantID: tenantID, RowLimit: int32(limit)})
	if err != nil {
		return AttentionSummary{}, fmt.Errorf("failed jobs: %w", err)
	}
	out.Failed = make([]AttentionJob, 0, len(failed))
	for _, r := range failed {
		out.Failed = append(out.Failed, AttentionJob{
			JobID: r.JobID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, FileName: r.FileName,
			TargetLanguage: r.TargetLanguage, Status: "failed", CurrentStep: r.CurrentStep,
			Detail: fmt.Sprintf("error: %s; %s", ptr.Deref(r.ErrorMessage), tsDetail("failed at", r.FailedAt)),
		})
	}

	hot, err := b.q.ListQAErrorHotspots(ctx, db.ListQAErrorHotspotsParams{TenantID: tenantID, RowLimit: int32(limit)})
	if err != nil {
		return AttentionSummary{}, fmt.Errorf("qa hotspots: %w", err)
	}
	out.QAHotspots = make([]AttentionJob, 0, len(hot))
	for _, r := range hot {
		out.QAHotspots = append(out.QAHotspots, AttentionJob{
			JobID: r.JobID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, FileName: r.FileName,
			TargetLanguage: r.TargetLanguage,
			Detail:         fmt.Sprintf("%d unresolved QA errors", r.ErrorCount),
		})
	}
	return out, nil
}

// --- Progress ---------------------------------------------------------------

const (
	defaultProgressProjects = 20
	maxProgressProjects     = 100
)

// GetProgress reports segment and word counts per project × target language,
// one page of projects at a time (newest first).
func (b *directBackend) GetProgress(ctx context.Context, in GetProgressIn) (ProgressReport, error) {
	tenantID, _ := scope(ctx)
	if in.ProjectID != 0 {
		if _, err := b.project(ctx, in.ProjectID); err != nil {
			return ProgressReport{}, err
		}
	}
	limit := clampInt(in.Limit, defaultProgressProjects, maxProgressProjects)
	ids, err := b.q.ListProgressProjectPage(ctx, db.ListProgressProjectPageParams{
		TenantID: tenantID, ProjectID: in.ProjectID, Cursor: in.Cursor, RowLimit: int32(limit),
	})
	if err != nil {
		return ProgressReport{}, fmt.Errorf("progress projects: %w", err)
	}
	out := ProgressReport{
		Rows: []ProgressRow{},
		Definition: "untranslated: no target text. pre_translated: TM or MT text nobody has edited yet. " +
			"draft: edited by a person but not confirmed. auto_approved: approved by quality prediction. " +
			"confirmed: confirmed by a person. The five buckets add up to total.",
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := b.q.GetTranslationProgress(ctx, db.GetTranslationProgressParams{TenantID: tenantID, ProjectIds: ids})
	if err != nil {
		return ProgressReport{}, fmt.Errorf("translation progress: %w", err)
	}
	for _, r := range rows {
		row := ProgressRow{
			ProjectID: r.ProjectID, ProjectName: r.ProjectName, TargetLanguage: r.TargetLanguage,
			Untranslated:  ProgressCounts{r.UntranslatedSegments, r.UntranslatedWords},
			PreTranslated: ProgressCounts{r.PreTranslatedSegments, r.PreTranslatedWords},
			Draft:         ProgressCounts{r.DraftSegments, r.DraftWords},
			AutoApproved:  ProgressCounts{r.AutoApprovedSegments, r.AutoApprovedWords},
			Confirmed:     ProgressCounts{r.ConfirmedSegments, r.ConfirmedWords},
			Total:         ProgressCounts{r.TotalSegments, r.TotalWords},
		}
		if r.TotalWords > 0 {
			row.PercentDoneWords = roundCents(float64(r.ConfirmedWords+r.AutoApprovedWords) * 100 / float64(r.TotalWords))
		}
		out.Rows = append(out.Rows, row)
	}
	if len(ids) == limit {
		out.HasMore = true
		out.NextCursor = ids[len(ids)-1]
	}
	return out, nil
}

// roundCents rounds to two decimal places.
func roundCents(v float64) float64 { return math.Round(v*100) / 100 }
