package timing

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Handler serves the per-project processing-time panel.
type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

// StageEntry is one stage's aggregate, flattened for the wire. Same numbers as
// the stored JSONB, plus a derived average so the client doesn't recompute it
// in three places.
type StageEntry struct {
	Stage string `json:"stage"`
	Group string `json:"group"`
	N     int64  `json:"n"`
	// SelfMS is the partitioning number: this stage's time with its children's
	// removed. Summing it across every stage equals the work exactly once.
	SelfMS int64 `json:"self_ms"`
	// TotalMS is inclusive of children. Equal to SelfMS for a leaf; larger for
	// a span that contains others (mt.db_save contains mt.tag_transfer, a
	// task.* span contains the whole pipeline). Never sum this.
	TotalMS int64             `json:"total_ms"`
	MaxMS   int64             `json:"max_ms"`
	AvgMS   int64             `json:"avg_ms"`
	Err     int64             `json:"err"`
	Meta    map[string]string `json:"meta,omitempty"`
	// Share of the rollup's total SELF time, 0–1. Sums to 1 across the list.
	Share float64 `json:"share"`
}

// JobTimingEntry is one job's breakdown.
type JobTimingEntry struct {
	JobID          int64        `json:"job_id"`
	FileID         int64        `json:"file_id"`
	FileName       string       `json:"file_name"`
	TargetLanguage string       `json:"target_language"`
	JobStatus      string       `json:"job_status"`
	WordCount      int32        `json:"word_count"`
	ComputeMS      int64        `json:"compute_ms"`
	QueueWaitMS    int64        `json:"queue_wait_ms"`
	Runs           int32        `json:"runs"`
	FirstStartedAt *string      `json:"first_started_at"`
	LastFinishedAt *string      `json:"last_finished_at"`
	Stages         []StageEntry `json:"stages"`
}

// ProjectTimingsResponse is the payload behind the project's Processing time
// panel: a project-wide rollup plus each job's own breakdown.
type ProjectTimingsResponse struct {
	// Totals across every tracked job in the project.
	TotalComputeMS   int64 `json:"total_compute_ms"`
	TotalQueueWaitMS int64 `json:"total_queue_wait_ms"`
	TotalRuns        int64 `json:"total_runs"`
	JobCount         int   `json:"job_count"`
	TotalWords       int64 `json:"total_words"`

	// Stage aggregate across all jobs, biggest first — the "where does the
	// time go" answer the panel leads with.
	//
	// ONE list, including the task.* spans, because self-time makes that safe:
	// every entry reports the time it alone is responsible for, so the shares
	// sum to 100% even though a task span physically contains the stages listed
	// beside it. A task span's self time is the uninstrumented overhead inside
	// that task.
	Stages []StageEntry `json:"stages"`

	Jobs []JobTimingEntry `json:"jobs"`
}

// HandleProjectTimings serves GET /api/projects/{projectID}/timings.
//
// Tenant scoping: the project is loaded with the tenant-scoped GetProject
// before anything else, so a cross-tenant projectID 404s before any timing row
// is read; the list query is independently tenant-scoped as well.
func (h *Handler) HandleProjectTimings(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	if _, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID: projectID, TenantID: tenantID,
	}); httputil.DBError(w, err, "project") {
		return
	}

	rows, err := h.queries.ListJobTimingsByProject(r.Context(), db.ListJobTimingsByProjectParams{
		ProjectID: projectID,
		TenantID:  tenantID,
	})
	if err != nil {
		logging.Error(r.Context(), err, "list job timings", "timing-list-failed",
			logging.ProjectID(projectID))
		httputil.ServerError(w, "load", "timings")
		return
	}

	resp := buildProjectTimings(rows)
	httputil.Data(w, resp)
}

// buildProjectTimings folds the per-job rows into the response shape. Split out
// of the handler so the aggregation is unit-testable without a request.
func buildProjectTimings(rows []db.ListJobTimingsByProjectRow) ProjectTimingsResponse {
	resp := ProjectTimingsResponse{
		Stages:   []StageEntry{},
		Jobs:     []JobTimingEntry{},
		JobCount: len(rows),
	}

	// Accumulate the project-wide stage totals while walking the jobs, so a
	// project with many jobs still costs one pass.
	total := map[string]*Stage{}

	for _, row := range rows {
		var stages map[string]Stage
		if len(row.Stages) > 0 {
			if err := json.Unmarshal(row.Stages, &stages); err != nil {
				// A row with an unreadable blob shouldn't blank the whole
				// panel — skip its stages, keep its totals.
				stages = nil
			}
		}

		entry := JobTimingEntry{
			JobID:          row.JobID,
			FileID:         row.FileID,
			FileName:       row.FileName,
			TargetLanguage: row.TargetLanguage,
			JobStatus:      row.JobStatus,
			WordCount:      row.WordCount,
			ComputeMS:      row.ComputeMs,
			QueueWaitMS:    row.QueueWaitMs,
			Runs:           row.Runs,
			FirstStartedAt: timestampPtr(row.FirstStartedAt),
			LastFinishedAt: timestampPtr(row.LastFinishedAt),
		}
		entry.Stages = sortedStages(stages)
		resp.Jobs = append(resp.Jobs, entry)

		resp.TotalComputeMS += row.ComputeMs
		resp.TotalQueueWaitMS += row.QueueWaitMs
		resp.TotalRuns += int64(row.Runs)
		resp.TotalWords += int64(row.WordCount)

		for slug, s := range stages {
			agg := total[slug]
			if agg == nil {
				agg = &Stage{}
				total[slug] = agg
			}
			agg.N += s.N
			agg.SelfMS += s.SelfMS
			agg.TotalMS += s.TotalMS
			agg.Err += s.Err
			if s.MaxMS > agg.MaxMS {
				agg.MaxMS = s.MaxMS
			}
			// Jobs that disagree on a meta value roll up as "mixed", so the UI
			// shows a value only when every contributing job agrees.
			for k, v := range s.Meta {
				if agg.Meta == nil {
					agg.Meta = map[string]string{}
				}
				if existing, seen := agg.Meta[k]; seen && existing != v {
					agg.Meta[k] = "mixed"
					continue
				}
				agg.Meta[k] = v
			}
		}
	}

	resp.Stages = sortedStages(derefStages(total))
	return resp
}

// sortedStages flattens a stage map into wire entries ordered by self time
// descending — the order the panel renders, so the client never has to sort.
// Ties break on slug so the output is deterministic for tests and for a UI
// that would otherwise reshuffle rows between polls.
func sortedStages(m map[string]Stage) []StageEntry {
	if len(m) == 0 {
		return []StageEntry{}
	}
	// Denominator is SELF time only — that is the quantity that partitions.
	// Using total here would count a task span's contents twice and push the
	// sum past 100%.
	var totalSelf int64
	for _, s := range m {
		totalSelf += s.SelfMS
	}

	out := make([]StageEntry, 0, len(m))
	for slug, s := range m {
		e := StageEntry{
			Stage:   slug,
			Group:   stageGroup(slug),
			N:       s.N,
			SelfMS:  s.SelfMS,
			TotalMS: s.TotalMS,
			MaxMS:   s.MaxMS,
			Err:     s.Err,
			Meta:    s.Meta,
		}
		// Average is inclusive — "how long does one of these take" is a
		// question about the whole call, not about the slice left after
		// subtracting children.
		if s.N > 0 {
			e.AvgMS = s.TotalMS / s.N
		}
		if totalSelf > 0 {
			e.Share = float64(s.SelfMS) / float64(totalSelf)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfMS != out[j].SelfMS {
			return out[i].SelfMS > out[j].SelfMS
		}
		return out[i].Stage < out[j].Stage
	})
	return out
}

func derefStages(m map[string]*Stage) map[string]Stage {
	out := make(map[string]Stage, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

// stageGroup is the slug's prefix ("mt.engine_call" → "mt"), which the panel
// uses to colour and band related stages together.
func stageGroup(slug string) string {
	for i := 0; i < len(slug); i++ {
		if slug[i] == '.' {
			return slug[:i]
		}
	}
	return slug
}

func timestampPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.UTC().Format(time.RFC3339)
	return &s
}
