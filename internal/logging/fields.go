// Package logging is OpenLoc's observability vocabulary: ECS-shaped structured
// logging plus a request-scoped field bag shared with error reporting.
//
// # Field naming
//
// Standard ECS field names are used wherever ECS defines the concept
// (service.*, log.level, event.action, error.*, http.*, trace.id, user.id).
// Those come free from the `ecs@mappings` component template in Elasticsearch.
// Domain entities that ECS has no opinion about live under `openloc.*`.
//
// No field duplicates an ECS concept under a custom prefix: OpenLoc ships to
// its own data stream, so ECS names are used as-is.
//
// All IDs are emitted as strings, matching the org convention and the
// `keyword` mappings in the index template. Numeric IDs in a strict-mapped
// data stream would be dropped to _source un-indexed on a type mismatch, so
// the Attr helpers below do the conversion — never hand-build these keys.
//
// # Sensitivity boundary
//
// Logs may contain customer content. Several call sites already log truncated
// segment text on purpose (internal/pipeline/tm_write.go, aistep_llm.go,
// translationmemory/handler_import.go) because it is essential for debugging
// translation defects, and logs stay inside the cluster in Elasticsearch.
//
// Error *reporting* is different: internal/errtrack ships to Honeybadger, a
// third party, and must never carry segment text. Do not treat "it's fine in a
// log line" as licence to put it in a notice context.
package logging

import (
	"log/slog"
	"strconv"
	"time"
)

// Standard ECS keys. Emitted as literal dotted keys; the ndjson parser on the
// Fleet filestream input runs with expand_keys:true, so "error.message"
// becomes a nested object on ingest.
const (
	KeyTimestamp = "@timestamp"
	KeyMessage   = "message"
	KeyLogLevel  = "log.level"

	KeyServiceName    = "service.name"
	KeyServiceVersion = "service.version"
	KeyServiceEnv     = "service.environment"

	// KeyEventAction is the stable machine-readable slug for a log line
	// ("pipeline-mt-failed"), as distinct from the human-readable message.
	// Same split the sibling services use.
	KeyEventAction = "event.action"

	KeyErrorMessage = "error.message"
	KeyErrorType    = "error.type"
	KeyErrorStack   = "error.stack_trace"

	KeyHTTPMethod = "http.request.method"
	KeyHTTPStatus = "http.response.status_code"

	KeyTraceID = "trace.id"
	KeyUserID  = "user.id"

	// KeyURLPath is the CONCRETE request path ("/api/jobs/42/mt-apply"), as
	// opposed to openloc.route which holds the chi pattern
	// ("/api/jobs/{jobID}/mt-apply"). Both are wanted: the pattern groups and
	// aggregates, the path tells you which resource actually failed.
	//
	// Path only — never the query string. The terminology lookup
	// (web/src/api/terminology.ts) puts customer segment text in ?source_text=,
	// so the query is untrusted (see internal/errtrack).
	KeyURLPath = "url.path"
)

// Domain keys under openloc.*. Each one needs a matching entry in the log
// index template, or it is stored but not searchable.
const (
	KeyTenantID          = "openloc.tenant.id"
	KeyUserRole          = "openloc.user.role"
	KeyProjectID         = "openloc.project.id"
	KeyJobID             = "openloc.job.id"
	KeyFileID            = "openloc.file.id"
	KeySegmentID         = "openloc.segment.id"
	KeyTranslationID     = "openloc.translation.id"
	KeyTMID              = "openloc.tm.id"
	KeyTermbaseID        = "openloc.termbase.id"
	KeyAnalysisID        = "openloc.analysis.id"
	KeyQAProfileID       = "openloc.qa_profile.id"
	KeyDashboardID       = "openloc.dashboard.id"
	KeyChartID           = "openloc.chart.id"
	KeyAIEngineID        = "openloc.ai_engine.id"
	KeyInviteID          = "openloc.invite.id"
	KeyIssueID           = "openloc.issue.id"
	KeyTMImportID        = "openloc.tm_import.id"
	KeySettingsProfileID = "openloc.settings_profile.id"

	KeySubsystem    = "openloc.subsystem"
	KeyRoute        = "openloc.route"
	KeyTaskType     = "openloc.task.type"
	KeyWorkflowStep = "openloc.workflow.step"
	KeySourceLang   = "openloc.lang.source"
	KeyTargetLang   = "openloc.lang.target"
	KeyEngine       = "openloc.engine"
	KeyProvider     = "openloc.provider"
	KeyStatus       = "openloc.status"
	KeyCount        = "openloc.count"
	KeyDurationMS   = "openloc.duration_ms"
)

// Escape-hatch prefixes, mirroring the org's two-tier convention.
//
//	LabelPrefix — flat strings, mapped and searchable. For one-off dimensions
//	              you want to filter on without amending the index template.
//	DebugPrefix — variable-shape blobs. Lands in _source un-indexed: visible in
//	              Kibana's document view, not filterable.
const (
	LabelPrefix = "openloc.labels."
	DebugPrefix = "openloc.debug."
)

// Label builds a searchable one-off string dimension.
func Label(name, value string) slog.Attr {
	return slog.String(LabelPrefix+name, value)
}

// Debugf builds an un-indexed blob field for arbitrary shapes.
func Debugf(name string, value any) slog.Attr {
	return slog.Any(DebugPrefix+name, value)
}

// --- Attr helpers -----------------------------------------------------------
//
// Prefer these over hand-written slog.String calls: they pin the key name and
// force IDs to strings so a strict-mapped field can't be silently dropped.

func id(key string, v int64) slog.Attr { return slog.String(key, strconv.FormatInt(v, 10)) }

func TenantID(v int64) slog.Attr          { return id(KeyTenantID, v) }
func UserID(v int64) slog.Attr            { return id(KeyUserID, v) }
func ProjectID(v int64) slog.Attr         { return id(KeyProjectID, v) }
func JobID(v int64) slog.Attr             { return id(KeyJobID, v) }
func FileID(v int64) slog.Attr            { return id(KeyFileID, v) }
func SegmentID(v int64) slog.Attr         { return id(KeySegmentID, v) }
func TranslationID(v int64) slog.Attr     { return id(KeyTranslationID, v) }
func TMID(v int64) slog.Attr              { return id(KeyTMID, v) }
func TermbaseID(v int64) slog.Attr        { return id(KeyTermbaseID, v) }
func AnalysisID(v int64) slog.Attr        { return id(KeyAnalysisID, v) }
func QAProfileID(v int64) slog.Attr       { return id(KeyQAProfileID, v) }
func DashboardID(v int64) slog.Attr       { return id(KeyDashboardID, v) }
func ChartID(v int64) slog.Attr           { return id(KeyChartID, v) }
func AIEngineID(v int64) slog.Attr        { return id(KeyAIEngineID, v) }
func InviteID(v int64) slog.Attr          { return id(KeyInviteID, v) }
func IssueID(v int64) slog.Attr           { return id(KeyIssueID, v) }
func TMImportID(v int64) slog.Attr        { return id(KeyTMImportID, v) }
func SettingsProfileID(v int64) slog.Attr { return id(KeySettingsProfileID, v) }

func UserRole(v string) slog.Attr     { return slog.String(KeyUserRole, v) }
func Subsystem(v string) slog.Attr    { return slog.String(KeySubsystem, v) }
func Route(v string) slog.Attr        { return slog.String(KeyRoute, v) }
func TaskType(v string) slog.Attr     { return slog.String(KeyTaskType, v) }
func WorkflowStep(v string) slog.Attr { return slog.String(KeyWorkflowStep, v) }
func Engine(v string) slog.Attr       { return slog.String(KeyEngine, v) }
func Provider(v string) slog.Attr     { return slog.String(KeyProvider, v) }
func Status(v string) slog.Attr       { return slog.String(KeyStatus, v) }
func TraceID(v string) slog.Attr      { return slog.String(KeyTraceID, v) }
func Count(v int) slog.Attr           { return slog.Int(KeyCount, v) }

func HTTPMethod(v string) slog.Attr { return slog.String(KeyHTTPMethod, v) }
func HTTPStatus(v int) slog.Attr    { return slog.Int(KeyHTTPStatus, v) }
func URLPath(v string) slog.Attr    { return slog.String(KeyURLPath, v) }

// IDFromString renders a URL path parameter as a keyword-typed ID field. Takes
// a string because chi hands URL params over unparsed, and the mapping targets
// the same `keyword` fields as the int64 helpers above.
func IDFromString(key, v string) slog.Attr { return slog.String(key, v) }

// SourceLang and TargetLang set one side of the pair. Use Langs below when both
// are known at once — the common case in the pipeline; these exist for the
// places that only have one, such as a job row, which carries a target language
// while the source lives on its project.
func SourceLang(v string) slog.Attr { return slog.String(KeySourceLang, v) }
func TargetLang(v string) slog.Attr { return slog.String(KeyTargetLang, v) }

// Langs emits the source and target language pair. Returned as a slice because
// the pair is almost always logged together; spread it into a call:
//
//	logging.Info(ctx, "...", "...", append(logging.Langs(sl, tl), logging.FileID(id))...)
func Langs(source, target string) []slog.Attr {
	return []slog.Attr{
		slog.String(KeySourceLang, source),
		slog.String(KeyTargetLang, target),
	}
}

// Duration records elapsed time in milliseconds. ECS prefers nanoseconds for
// event.duration, but every existing call site reports human-scale timings, so
// ms keeps dashboards readable and matches the org's `*_duration_s` habit.
func Duration(d time.Duration) slog.Attr {
	return slog.Int64(KeyDurationMS, d.Milliseconds())
}
