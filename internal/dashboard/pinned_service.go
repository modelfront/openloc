package dashboard

// The rules of the Pinned view. A pinned chart is a reference, one per person
// and chart; a pinned dashboard is the person's own collection of them, made
// from the chart's dashboard (its source) and named after it. Pinning a whole
// dashboard makes its pinned dashboard follow the source: every live chart of
// the source shows there, including ones added later. Unpinning, moving or
// reordering one of those charts first turns the followed charts into
// references, so nothing the person can see changes underfoot.
//
// Every rule runs in one transaction (pinTx) and works only on rows of the
// caller's tenant and user.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// errNotPinned is a move or reorder naming a chart that is not on the pinned
// dashboard it was said to be on.
var errNotPinned = errors.New("chart is not pinned")

type pinScope struct {
	q        db.Querier
	tenantID int64
	userID   int64
}

func (h *Handler) pinTx(ctx context.Context, tenantID, userID int64, fn func(ps pinScope) error) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(pinScope{q: db.New(tx), tenantID: tenantID, userID: userID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// pinnedFor returns the person's pinned dashboard made from source, creating
// it — named after the source, with the source's filter row — when there is
// none.
func (ps pinScope) pinnedFor(ctx context.Context, source db.GetDashboardTenantRow) (int64, error) {
	p, err := ps.q.GetPinnedDashboardForSource(ctx, db.GetPinnedDashboardForSourceParams{
		TenantID: ps.tenantID, UserID: ps.userID, SourceDashboardID: &source.ID,
	})
	if err == nil {
		return p.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("find pinned dashboard: %w", err)
	}
	filters := source.Filters
	if len(filters) == 0 {
		filters = []byte(`{"controls":[]}`)
	}
	id, err := ps.q.CreatePinnedDashboard(ctx, db.CreatePinnedDashboardParams{
		TenantID: ps.tenantID, UserID: ps.userID, Name: source.Name,
		SourceDashboardID: &source.ID, Filters: filters,
	})
	if err != nil {
		return 0, fmt.Errorf("create pinned dashboard: %w", err)
	}
	return id, nil
}

// charts lists one pinned dashboard's charts (0: all of them), as shown.
func (ps pinScope) charts(ctx context.Context, pinnedID int64) ([]db.ListPinnedChartsRow, error) {
	rows, err := ps.q.ListPinnedCharts(ctx, db.ListPinnedChartsParams{
		TenantID: ps.tenantID, UserID: &ps.userID, PinnedID: pinnedID,
	})
	if err != nil {
		return nil, fmt.Errorf("list pinned charts: %w", err)
	}
	return rows, nil
}

// materialize turns the charts a pinned dashboard shows by following its
// source into references, in the order shown.
func (ps pinScope) materialize(ctx context.Context, pinnedID int64) error {
	rows, err := ps.charts(ctx, pinnedID)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if c.Referenced {
			continue
		}
		if err := ps.q.AddPinnedChart(ctx, db.AddPinnedChartParams{
			PinnedDashboardID: pinnedID, UserID: ps.userID, ChartID: c.ID,
		}); err != nil {
			return fmt.Errorf("pin followed chart: %w", err)
		}
	}
	return nil
}

// stopFollowing fixes a pinned dashboard to the charts it shows now.
func (ps pinScope) stopFollowing(ctx context.Context, pinnedID int64) error {
	if err := ps.materialize(ctx, pinnedID); err != nil {
		return err
	}
	return ps.q.SetPinnedDashboardFollows(ctx, db.SetPinnedDashboardFollowsParams{
		ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID, FollowsSource: false,
	})
}

// dropIfEmpty removes a pinned dashboard that shows no chart and follows no
// source: nothing is created in the Pinned view by hand, so an empty one has
// no reason to stay.
func (ps pinScope) dropIfEmpty(ctx context.Context, pinnedID int64) error {
	p, err := ps.q.GetPinnedDashboard(ctx, db.GetPinnedDashboardParams{ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get pinned dashboard: %w", err)
	}
	if p.FollowsSource {
		return nil
	}
	rows, err := ps.charts(ctx, pinnedID)
	if err != nil || len(rows) > 0 {
		return err
	}
	return ps.remove(ctx, pinnedID)
}

// remove deletes a pinned dashboard and its references.
func (ps pinScope) remove(ctx context.Context, pinnedID int64) error {
	if err := ps.q.DeletePinnedChartsOfPinned(ctx, db.DeletePinnedChartsOfPinnedParams{
		ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID,
	}); err != nil {
		return fmt.Errorf("unpin charts: %w", err)
	}
	if err := ps.q.SoftDeletePinnedDashboard(ctx, db.SoftDeletePinnedDashboardParams{
		ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID,
	}); err != nil {
		return fmt.Errorf("delete pinned dashboard: %w", err)
	}
	return nil
}

// whereShown is the pinned dashboard a chart shows on, by reference or by
// following its source; 0 when it is not pinned.
func (ps pinScope) whereShown(ctx context.Context, chartID int64) (int64, error) {
	rows, err := ps.charts(ctx, 0)
	if err != nil {
		return 0, err
	}
	for _, c := range rows {
		if c.ID == chartID {
			return c.PinnedDashboardID, nil
		}
	}
	return 0, nil
}

// pinChart adds a reference to the chart on the pinned dashboard made from the
// chart's dashboard. Already pinned is not an error.
func (ps pinScope) pinChart(ctx context.Context, chart db.GetDashboardChartTenantRow, source db.GetDashboardTenantRow) error {
	at, err := ps.whereShown(ctx, chart.ID)
	if err != nil || at != 0 {
		return err
	}
	pinnedID, err := ps.pinnedFor(ctx, source)
	if err != nil {
		return err
	}
	return ps.q.AddPinnedChart(ctx, db.AddPinnedChartParams{PinnedDashboardID: pinnedID, UserID: ps.userID, ChartID: chart.ID})
}

// unpinChart removes the chart from the Pinned view, wherever it shows. The
// pinned dashboard no longer holds all of its source, so it stops following
// first: the other charts stay, as references, and the source cannot bring
// this one back.
func (ps pinScope) unpinChart(ctx context.Context, chartID int64) error {
	at, err := ps.whereShown(ctx, chartID)
	if err != nil || at == 0 {
		return err
	}
	if err := ps.stopFollowing(ctx, at); err != nil {
		return err
	}
	if err := ps.q.DeletePinnedChart(ctx, db.DeletePinnedChartParams{UserID: ps.userID, ChartID: chartID, TenantID: ps.tenantID}); err != nil {
		return fmt.Errorf("unpin chart: %w", err)
	}
	return ps.dropIfEmpty(ctx, at)
}

// pinDashboard makes the dashboard's pinned dashboard follow it. Its charts
// already pinned elsewhere in the view stay where the person put them.
func (ps pinScope) pinDashboard(ctx context.Context, source db.GetDashboardTenantRow) error {
	pinnedID, err := ps.pinnedFor(ctx, source)
	if err != nil {
		return err
	}
	return ps.q.SetPinnedDashboardFollows(ctx, db.SetPinnedDashboardFollowsParams{
		ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID, FollowsSource: true,
	})
}

// unpinDashboard removes every chart of the dashboard from the Pinned view and
// stops following it.
func (ps pinScope) unpinDashboard(ctx context.Context, dashboardID int64) error {
	rows, err := ps.charts(ctx, 0)
	if err != nil {
		return err
	}
	touched := map[int64]bool{}
	for _, c := range rows {
		if c.DashboardID == dashboardID {
			touched[c.PinnedDashboardID] = true
		}
	}
	if p, err := ps.q.GetPinnedDashboardForSource(ctx, db.GetPinnedDashboardForSourceParams{
		TenantID: ps.tenantID, UserID: ps.userID, SourceDashboardID: &dashboardID,
	}); err == nil {
		touched[p.ID] = true
		if err := ps.q.SetPinnedDashboardFollows(ctx, db.SetPinnedDashboardFollowsParams{
			ID: p.ID, TenantID: ps.tenantID, UserID: ps.userID, FollowsSource: false,
		}); err != nil {
			return fmt.Errorf("stop following: %w", err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find pinned dashboard: %w", err)
	}
	if err := ps.q.DeletePinnedChartsOfDashboard(ctx, db.DeletePinnedChartsOfDashboardParams{
		UserID: ps.userID, TenantID: ps.tenantID, DashboardID: dashboardID,
	}); err != nil {
		return fmt.Errorf("unpin dashboard charts: %w", err)
	}
	for id := range touched {
		if err := ps.dropIfEmpty(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// moveChart puts a pinned chart on another of the person's pinned dashboards.
// The one it leaves no longer holds all of its source, so it stops following.
func (ps pinScope) moveChart(ctx context.Context, chartID, toPinnedID int64) error {
	if _, err := ps.q.GetPinnedDashboard(ctx, db.GetPinnedDashboardParams{ID: toPinnedID, TenantID: ps.tenantID, UserID: ps.userID}); err != nil {
		return err
	}
	at, err := ps.whereShown(ctx, chartID)
	if err != nil {
		return err
	}
	if at == 0 {
		return errNotPinned
	}
	if at == toPinnedID {
		return nil
	}
	if err := ps.stopFollowing(ctx, at); err != nil {
		return err
	}
	if _, err := ps.q.MovePinnedChart(ctx, db.MovePinnedChartParams{
		ToPinnedID: toPinnedID, UserID: ps.userID, ChartID: chartID, TenantID: ps.tenantID,
	}); err != nil {
		return fmt.Errorf("move pinned chart: %w", err)
	}
	return ps.dropIfEmpty(ctx, at)
}

// reorderCharts saves the order of one pinned dashboard's charts. Followed
// charts become references first, so the order sticks; the dashboard keeps
// following, and charts added to the source later go to the end.
func (ps pinScope) reorderCharts(ctx context.Context, pinnedID int64, chartIDs []int64) error {
	if _, err := ps.q.GetPinnedDashboard(ctx, db.GetPinnedDashboardParams{ID: pinnedID, TenantID: ps.tenantID, UserID: ps.userID}); err != nil {
		return err
	}
	if err := ps.materialize(ctx, pinnedID); err != nil {
		return err
	}
	for i, id := range chartIDs {
		if err := ps.q.ReorderPinnedChart(ctx, db.ReorderPinnedChartParams{
			PinnedDashboardID: pinnedID, UserID: ps.userID, ChartID: id, Position: int32(i + 1),
		}); err != nil {
			return fmt.Errorf("reorder pinned chart: %w", err)
		}
	}
	return nil
}

// pinState is what the pin icons show: the charts in the Pinned view, and the
// dashboards whose every visible chart is there (or that are followed).
func (ps pinScope) pinState(ctx context.Context) (charts, dashboards []int64, err error) {
	rows, err := ps.charts(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	pinned := make(map[int64]bool, len(rows))
	charts = make([]int64, 0, len(rows))
	for _, c := range rows {
		pinned[c.ID] = true
		charts = append(charts, c.ID)
	}
	visible, err := ps.q.ListVisibleChartDashboards(ctx, db.ListVisibleChartDashboardsParams{TenantID: ps.tenantID, UserID: &ps.userID})
	if err != nil {
		return nil, nil, fmt.Errorf("list visible charts: %w", err)
	}
	all := map[int64]bool{}
	for _, v := range visible {
		if _, seen := all[v.DashboardID]; !seen {
			all[v.DashboardID] = true
		}
		if !pinned[v.ID] {
			all[v.DashboardID] = false
		}
	}
	followed, err := ps.q.ListPinnedDashboards(ctx, db.ListPinnedDashboardsParams{TenantID: ps.tenantID, UserID: ps.userID})
	if err != nil {
		return nil, nil, fmt.Errorf("list pinned dashboards: %w", err)
	}
	for _, p := range followed {
		if p.FollowsSource && p.SourceDashboardID != nil {
			all[*p.SourceDashboardID] = true
		}
	}
	dashboards = make([]int64, 0, len(all))
	for id, ok := range all {
		if ok {
			dashboards = append(dashboards, id)
		}
	}
	return charts, dashboards, nil
}
