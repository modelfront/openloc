package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"openloc/internal/analysis"
	"openloc/internal/analytics"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/invite"
	"openloc/internal/job"
	"openloc/internal/ptr"
)

// InsightDeps is what the analysis and invite tools need beyond the Querier.
// Every field is optional: with no enqueuer a saved analysis stays pending,
// and with no invite service the invite tool refuses. Invites is the process's
// one invite.Service, shared with the REST handlers for its email budget.
type InsightDeps struct {
	AnalysisEnqueuer analysis.Enqueuer
	Invites          *invite.Service
}

// WithInsightDeps attaches InsightDeps to a backend built by this package.
func WithInsightDeps(b Backend, d InsightDeps) Backend {
	if direct, ok := b.(*directBackend); ok {
		direct.insight = d
	}
	return b
}

// insightRole is the caller's role in the current org.
func insightRole(ctx context.Context) string {
	if c, ok := auth.ClaimsFromContext(ctx); ok {
		return c.Role
	}
	return ""
}

// insightNotFound maps a tenant-scoped miss onto the opaque errNotFound.
func insightNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	return err
}

// --- Analysis ---------------------------------------------------------------

func (b *directBackend) GetProjectAnalysis(ctx context.Context, in GetProjectAnalysisIn) (job.ProjectAnalysis, error) {
	tenantID, _ := scope(ctx)
	res, err := job.ComputeProjectAnalysis(ctx, b.q, tenantID, in.ProjectID)
	if err != nil {
		return job.ProjectAnalysis{}, insightNotFound(err)
	}
	return res, nil
}

func savedAnalysisView(a db.Analysis) SavedAnalysis {
	ids := a.JobIds
	if ids == nil {
		ids = []int64{}
	}
	return SavedAnalysis{
		ID:           a.ID,
		ProjectID:    a.ProjectID,
		Name:         a.Name,
		Status:       string(a.Status),
		JobIDs:       ids,
		IsAuto:       a.IsAuto,
		ErrorMessage: ptr.Deref(a.ErrorMessage),
		CreatedAt:    tsString(a.CreatedAt),
		CompletedAt:  tsString(a.CompletedAt),
	}
}

func (b *directBackend) ListSavedAnalyses(ctx context.Context, in ListSavedAnalysesIn) (SavedAnalysisList, error) {
	tenantID, _ := scope(ctx)
	if in.AnalysisID != 0 {
		a, breakdowns, err := analysis.Get(ctx, b.q, tenantID, in.AnalysisID)
		if err != nil {
			return SavedAnalysisList{}, insightNotFound(err)
		}
		if a.ProjectID != in.ProjectID {
			return SavedAnalysisList{}, errNotFound
		}
		v := savedAnalysisView(a)
		v.Breakdowns = make([]SavedAnalysisBreakdown, 0, len(breakdowns))
		for _, r := range breakdowns {
			v.Breakdowns = append(v.Breakdowns, SavedAnalysisBreakdown{
				JobID: r.JobID, JobName: r.JobNameSnapshot, TargetLanguage: r.TargetLanguage,
				Tm101Words: r.Tm101Words, Tm100Words: r.Tm100Words, Tm8599Words: r.Tm8599Words,
				Tm6584Words: r.Tm6584Words, NewWords: r.NewWords, RepeatedWords: r.RepetitionsWords,
			})
		}
		return SavedAnalysisList{Analyses: []SavedAnalysis{v}, Count: 1}, nil
	}
	rows, err := analysis.List(ctx, b.q, tenantID, in.ProjectID)
	if err != nil {
		return SavedAnalysisList{}, insightNotFound(err)
	}
	out := make([]SavedAnalysis, 0, len(rows))
	for _, a := range rows {
		out = append(out, savedAnalysisView(a))
	}
	return SavedAnalysisList{Analyses: out, Count: len(out)}, nil
}

func (b *directBackend) SaveAnalysis(ctx context.Context, in SaveAnalysisIn) (SavedAnalysis, error) {
	tenantID, userID := scope(ctx)
	a, err := analysis.Create(ctx, b.q, b.insight.AnalysisEnqueuer, tenantID, userID,
		analysis.CreateInput{ProjectID: in.ProjectID, Name: in.Name, JobIDs: in.JobIDs})
	var inputErr analysis.InputError
	switch {
	case err == nil:
		return savedAnalysisView(a), nil
	case errors.As(err, &inputErr):
		return SavedAnalysis{}, invalidf("%s", inputErr.Msg)
	default:
		return SavedAnalysis{}, insightNotFound(err)
	}
}

func (b *directBackend) PreviewDeleteSavedAnalysis(ctx context.Context, in DeleteSavedAnalysisIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	a, breakdowns, err := analysis.Get(ctx, b.q, tenantID, in.AnalysisID)
	if err != nil {
		return Preview{}, insightNotFound(err)
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the saved analysis “%s” (%s).", a.Name, a.Status),
		Effects: []string{fmt.Sprintf("Its %d per-job breakdown rows leave the Analyses tab. The project's live analysis is not affected.", len(breakdowns))},
	}, nil
}

func (b *directBackend) DeleteSavedAnalysis(ctx context.Context, in DeleteSavedAnalysisIn) (SavedAnalysisDeleted, error) {
	tenantID, _ := scope(ctx)
	if err := analysis.Delete(ctx, b.q, tenantID, in.AnalysisID); err != nil {
		return SavedAnalysisDeleted{}, insightNotFound(err)
	}
	return SavedAnalysisDeleted{AnalysisID: in.AnalysisID, Deleted: true}, nil
}

// --- Analytics --------------------------------------------------------------

func (b *directBackend) GetProductivity(ctx context.Context, in GetProductivityIn) (ProductivityReport, error) {
	tenantID, _ := scope(ctx)
	out := ProductivityReport{Scope: in.Scope, Rows: []analytics.ProductivityEntry{}, Steps: []analytics.StepProductivityEntry{}}
	switch in.Scope {
	case "org":
		from, to := analytics.DefaultProductivityRange(time.Now())
		if in.From != "" {
			t, err := time.Parse("2006-01-02", in.From)
			if err != nil {
				return ProductivityReport{}, invalidf("'from' must be YYYY-MM-DD")
			}
			from = t
		}
		if in.To != "" {
			t, err := time.Parse("2006-01-02", in.To)
			if err != nil {
				return ProductivityReport{}, invalidf("'to' must be YYYY-MM-DD")
			}
			to = t
		}
		rows, err := analytics.OrgProductivity(ctx, b.q, tenantID, from, to)
		if err != nil {
			return ProductivityReport{}, err
		}
		out.Rows, out.From, out.To = rows, from.Format("2006-01-02"), to.Format("2006-01-02")
	case "project", "project_by_step":
		if in.ProjectID == 0 {
			return ProductivityReport{}, invalidf("project_id is required for scope %s", in.Scope)
		}
		if in.Scope == "project" {
			rows, err := analytics.ProjectProductivity(ctx, b.q, tenantID, in.ProjectID)
			if err != nil {
				return ProductivityReport{}, insightNotFound(err)
			}
			out.Rows = rows
		} else {
			steps, err := analytics.ProjectStepProductivity(ctx, b.q, tenantID, in.ProjectID)
			if err != nil {
				return ProductivityReport{}, insightNotFound(err)
			}
			out.Steps = steps
		}
	default:
		return ProductivityReport{}, invalidf("scope must be org, project or project_by_step")
	}
	return out, nil
}
