package demodata

// Sample Processing-time panel data, shared by `make seed`
// and the demo organization. The stages go through the real timing.Recorder
// and the real merge query, so a change to either shape shows up here.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/timing"
)

// WriteJobTimings merges a synthetic pipeline run into each job's job_timings
// row. The shape varies per job (MT-heavy, QE+APE-heavy, one failed MT chunk)
// so the panel shows a distribution. The merge accumulates, so callers that
// run twice clear the rows first.
func WriteJobTimings(ctx context.Context, q db.Querier, tenantID int64, jobIDs []int64) error {
	for i, jobID := range jobIDs {
		stages := syntheticRun(i).Stages()
		var compute int64
		for _, s := range stages {
			compute += s.SelfMS
		}
		payload, err := json.Marshal(stages)
		if err != nil {
			return fmt.Errorf("encode timings for job %d: %w", jobID, err)
		}
		// compute_ms is the sum of self times: the identity a real run keeps.
		if err := q.UpsertJobTiming(ctx, db.UpsertJobTimingParams{
			JobID: jobID, TenantID: tenantID, Stages: payload,
			ComputeMs: compute, QueueWaitMs: int64(250 + i*180),
		}); err != nil {
			return fmt.Errorf("job timings for job %d: %w", jobID, err)
		}
	}
	return nil
}

// syntheticRun records the i-th job's stages.
func syntheticRun(i int) *timing.Recorder {
	segments := 40 + i*35
	chunks := segments/30 + 1
	rec := timing.New()

	// Ingest is one span per upload, fanned out to every job it created.
	rec.Record(timing.StageIngest, time.Duration(400+i*40)*time.Millisecond, nil, timing.Meta("format", "docx"))

	rec.Record(timing.StageTMContextMatch, time.Duration(30+i*8)*time.Millisecond, nil)
	for b := 0; b < 2; b++ {
		rec.Record(timing.StageTMExactLookup, time.Duration(45+b*12)*time.Millisecond, nil)
	}
	for f := 0; f < segments/4; f++ {
		rec.Record(timing.StageTMFuzzyLookup, time.Duration(7+f%5)*time.Millisecond, nil)
	}
	for a := 0; a < segments/6; a++ {
		rec.Record(timing.StageTMApply, time.Duration(3+a%3)*time.Millisecond, nil)
	}

	// MT is the headline cost. The second job has one failed chunk.
	engine := "libretranslate"
	if i%3 == 1 {
		engine = "deepl"
	}
	for c := 0; c < chunks; c++ {
		d := time.Duration(1400+c*250+i*120) * time.Millisecond
		var chunkErr error
		if i == 1 && c == 1 {
			chunkErr = fmt.Errorf("simulated upstream 502")
			d = 4200 * time.Millisecond
		}
		rec.Record(timing.StageMTEngineCall, d, chunkErr, timing.Meta("engine", engine), timing.Meta("tag_aware", "false"))
		rec.Record(timing.StageMTDBSave, time.Duration(55+c*6)*time.Millisecond, nil)
		rec.Record(timing.StageMTTagTransfer, time.Duration(9+c)*time.Millisecond, nil)
	}

	// QE+APE alternates the backend so both slugs appear in the rollup.
	batches := segments/30 + 1
	for b := 0; b < batches; b++ {
		if i%2 == 0 {
			rec.Record(timing.StageAIPredictExtension, time.Duration(700+b*90)*time.Millisecond, nil, timing.Meta("source", "ext:mockqe"))
		} else {
			rec.Record(timing.StageAIPredictLLM, time.Duration(2100+b*300)*time.Millisecond, nil,
				timing.Meta("source", "anthropic"), timing.Meta("model", "claude-opus-5"))
		}
		rec.Record(timing.StageAIRouteApply, time.Duration(40+b*5)*time.Millisecond, nil)
	}

	// Task spans carry SELF time: a whole-task duration here would count every
	// stage above twice.
	rec.Record(timing.StageTaskPipeline, time.Duration(120+i*40)*time.Millisecond, nil)
	rec.Record(timing.StageTaskAIStep, time.Duration(60+i*20)*time.Millisecond, nil)
	return rec
}
