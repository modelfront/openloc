package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// Tour picks let a saved tour open real pages — a project,
// a job in the editor, a prompt, a dashboard — although their ids differ per
// organization. Each pick names the page it opens and a tenant-scoped query
// that chooses the item. A step may also name another page that takes the
// same ids; it opens that page.

type tourPick struct {
	page string
}

var tourPicks = map[string]tourPick{
	"active_job": {page: "job_editor"},
	// The organization's own prompt of that purpose, else a built-in one.
	"mt_prompt": {page: "mt_prompt"},
	"ai_prompt": {page: "ai_prompt"},
	// The shared dashboard with the most charts, and its first chart.
	"latest_dashboard": {page: "dashboard"},
	"dashboard_chart":  {page: "dashboard_chart"},
}

// pickFunc resolves a pick to the page's ids; found is false when the
// organization has no such item.
type pickFunc func(pick string) (ids map[string]int64, found bool, err error)

// tenantPicker resolves picks for the caller, once per pick.
func tenantPicker(ctx context.Context, q db.Querier, claims *auth.Claims) pickFunc {
	cache := map[string]map[string]int64{}
	return func(pick string) (map[string]int64, bool, error) {
		if ids, ok := cache[pick]; ok {
			return ids, ids != nil, nil
		}
		ids, err := resolvePick(ctx, q, claims, pick)
		if errors.Is(err, pgx.ErrNoRows) {
			cache[pick] = nil
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("tour pick %s: %w", pick, err)
		}
		cache[pick] = ids
		return ids, true, nil
	}
}

func resolvePick(ctx context.Context, q db.Querier, claims *auth.Claims, pick string) (map[string]int64, error) {
	tenantID := claims.TenantID
	switch pick {
	case "active_job":
		row, err := q.PickTourJob(ctx, tenantID)
		return map[string]int64{"project_id": row.ProjectID, "job_id": row.JobID}, err
	case "mt_prompt", "ai_prompt":
		purpose := "qe_ape"
		if pick == "mt_prompt" {
			purpose = "mt"
		}
		id, err := q.PickTourPrompt(ctx, db.PickTourPromptParams{TenantID: tenantID, Purpose: purpose})
		return map[string]int64{"prompt_id": id}, err
	case "latest_dashboard":
		row, err := q.PickTourDashboard(ctx, tenantID)
		return map[string]int64{"dashboard_id": row.DashboardID}, err
	case "dashboard_chart":
		row, err := q.PickTourDashboard(ctx, tenantID)
		return map[string]int64{"dashboard_id": row.DashboardID, "chart_id": row.ChartID}, err
	}
	return nil, fmt.Errorf("unknown pick %q", pick)
}
