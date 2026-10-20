// Package timing records how long each stage of job processing takes.
//
// # Why aggregates, not events
//
// The pipeline's expensive stages are loops: MT translates 50 segments a call,
// a QE+APE predictor scores up to 30 rows a batch. A 1000-segment job runs the MT
// engine ~20 times and the predictor ~34 times, so a row-per-occurrence table
// would grow by hundreds of rows per job and need a GROUP BY before it could
// answer anything. Instead a Recorder accumulates in memory for the life of one
// task and flushes once into a single job_timings row that merges on write.
// Per-occurrence detail is not lost — every span also emits a
// structured log line carrying logging.Duration, so drill-down lives in
// Elasticsearch while Postgres holds the aggregate a UI can render directly.
//
// # Never counting the same millisecond twice
//
// Spans nest. `mt.tag_transfer` runs inside `mt.db_save`; every `task.*` span
// wraps the entire pipeline. Recording one duration per stage and summing the
// map therefore counts nested time under several names, and a stage's share of
// the work reads lower than it is because the spans containing it count as its
// peers.
//
// So spans form a tree, threaded through the context, and each records two
// numbers:
//
//	TotalMS — inclusive: the whole span, children and all.
//	SelfMS  — exclusive: total minus what its direct children accounted for.
//
// Only SelfMS may be summed, and summing it always yields the wall clock
// exactly once regardless of nesting depth. Double-counting is arithmetically
// impossible rather than forbidden by convention — which matters because the
// slug list below is open, and the next person to wrap one span around another
// shouldn't have to know this rule for it to hold. A task span's SelfMS falls
// out as the uninstrumented overhead, so it needs no special case.
//
// # Using it
//
// A unit of work installs a recorder, instruments its stages, and flushes:
//
//	ctx, rec := timing.WithRecorder(ctx)
//	defer rec.Flush(ctx, queries, tenantID, jobID)
//
//	ctx, done := timing.Start(ctx, timing.StageMTEngineCall, timing.Meta("engine", name))
//	results, err := engine.Translate(ctx, texts)
//	done(err)
//
// Thread Start's returned context into the work being measured — that is what
// makes a nested span a child rather than a double-counting sibling. Do it even
// for spans with no children today; it costs nothing and keeps the guarantee
// when one is added.
//
// Start is a no-op when the context carries no recorder, so instrumenting a
// function never constrains where it can be called from — HTTP handlers and
// tests that don't care about timings pay a context lookup and nothing else.
package timing

// Stage slugs. Dotted, group-prefixed, and stable: they become JSON object keys
// in job_timings.stages and event.action values in logs, so renaming one
// orphans historical data. Adding one is free — the schema doesn't enumerate
// them.
const (
	// --- Ingest. File-scoped work, fanned out to every job the upload creates,
	// so one job's row explains its time from the user's click onward rather
	// than starting mid-pipeline.
	//
	// ONE slug covering parse + storage upload + word count + segment/
	// translation insert + job creation: each piece is under 1% of a job, so
	// separate rows would be noise. Parse carries a `format` tag.
	StageIngest = "ingest"

	// --- TM phase (internal/pipeline/translate.go runTMPhase).
	//
	// Constant-time lookups (TM assignment resolution) get no slug; their time
	// falls into the enclosing task span's self time. tm.context_match gets one
	// because it is a whole-file segment scan plus an md5 per segment, so it
	// grows with the document.
	StageTMExactLookup  = "tm.exact_lookup"
	StageTMFuzzyLookup  = "tm.fuzzy_lookup"
	StageTMContextMatch = "tm.context_match"
	StageTMApply        = "tm.apply"

	// --- MT phase (internal/pipeline/translate.go runMTPhase).
	// Engine resolution is one constant-time lookup and gets no slug.
	StageMTEngineCall  = "mt.engine_call"
	StageMTTagTransfer = "mt.tag_transfer"
	StageMTDBSave      = "mt.db_save"

	// --- QE+APE step (internal/pipeline/aistep.go RunAIStep).
	StageAIPredictExtension = "ai.predict_extension"
	StageAIPredictLLM       = "ai.predict_llm"
	StageAIRouteApply       = "ai.route_apply"

	// --- TM writeback on completion (internal/pipeline/tmwrite.go).
	// The key's "feedback." prefix is stored in job_timings rows.
	StageCompletionTMWrite = "feedback.tm_writeback"

	// --- QA.
	StageQASpellcheck = "qa.spellcheck"

	// --- Whole-task spans, recorded by the asynq handlers. Summing n across
	// these gives the number of times a job has been worked on; summing ms
	// gives compute_ms.
	StageTaskTranslate = "task.translate"
	StageTaskAIStep    = "task.ai_step"
	StageTaskPipeline  = "task.pipeline"
)
