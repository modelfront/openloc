package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/ai"
	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	"openloc/internal/aiusage"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/extension"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	"openloc/internal/mt"
	"openloc/internal/mtengine"
	"openloc/internal/mtrouter"
	"openloc/internal/ptr"
	"openloc/internal/settings"
	"openloc/internal/timing"
	"openloc/internal/translationmemory"
)

// FileStorage is the minimal slice of storage behavior the pipeline needs
// (just Download for the export path). Defining it here instead of importing
// `internal/file` keeps the dependency arrow one-way — file → pipeline —
// which lets the upload hook in file/handler.go reference pipeline types
// (e.g. TranslatePhases) without an import cycle.
type FileStorage interface {
	Download(ctx context.Context, path string) (io.ReadCloser, error)
}

// llmPredictBatchSize is the most rows per LLM QE+APE request. An extension
// sets its own batch size in its manifest.
const llmPredictBatchSize = 30

// mtChunkSize is the maximum number of segments per MT engine call.
// Chunking enables incremental DB saves and progress tracking. 30 keeps
// progress updates responsive; bigger chunks don't help because LibreTranslate
// works through a batch one segment at a time, so they only delay progress.
// For throughput, scale horizontally (multiple LibreTranslate replicas).
const mtChunkSize = 30

// tmEngineName is the marker stored in translations.mt_engine for TM-sourced
// matches (exact or fuzzy) so the UI / analytics can distinguish them from
// MT-sourced ones ("mock", "libretranslate", …).
const tmEngineName = "tm"

// tmExactBatchSize bounds the source-text array passed to LookupTMBatch.
// 500 keeps the pgx parameter count well under the 65k limit.
const tmExactBatchSize = 500

// ProgressFunc is called to report pipeline progress.
// phase: "tm" (TM lookup) or "mt" (MT translation)
// done: segments completed in this phase
// total: total segments in this phase
type ProgressFunc func(phase string, done, total int)

// TxRunner abstracts "run f within a transaction." Production uses
// PgxTxRunner backed by *pgxpool.Pool; tests can supply a passthrough
// implementation that runs the callback against a mock querier without
// opening a real tx.
//
// The callback receives a db.Querier bound to the transaction (or to
// whatever underlying mock the test supplies). Implementations are
// responsible for commit-on-success / rollback-on-error semantics.
type TxRunner interface {
	InTx(ctx context.Context, fn func(db.Querier) error) error
}

// PgxTxRunner is the production TxRunner backed by a real connection pool.
// Each InTx call opens a tx, runs the callback with a tx-bound db.Querier,
// and commits if the callback succeeds (rolls back on error).
type PgxTxRunner struct {
	pool *pgxpool.Pool
}

// NewPgxTxRunner constructs a TxRunner backed by the given pool. Panics on
// nil pool — production wiring should never reach this with a missing dep.
func NewPgxTxRunner(pool *pgxpool.Pool) *PgxTxRunner {
	if pool == nil {
		panic("pipeline.NewPgxTxRunner: pool must be non-nil")
	}
	return &PgxTxRunner{pool: pool}
}

// InTx runs fn inside a database transaction, committing on success and
// rolling back on error.
func (r *PgxTxRunner) InTx(ctx context.Context, fn func(db.Querier) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Service struct {
	queries db.Querier
	txr     TxRunner // SaveSegmentToTM uses this to wrap upsert + history insert in one tx
	storage FileStorage

	// engineFactory creates an mt.Engine from a (type, apiKey, apiURL, settings) tuple.
	// Production wires mt.NewFromDB. In-package tests use newService to swap
	// in instrumented engines (callCountEngine, failAfterNEngine) without
	// round-tripping through the DB factory. Never nil — constructors
	// initialize it.
	engineFactory func(engineType, apiKey, apiURL string, settings map[string]any) (mt.Engine, error)

	// Must be the same Scorer the sidebar uses or pipeline-applied and
	// sidebar percents will disagree on the same TM hit.
	scorer translationmemory.Scorer

	// AI-as-MT. Both nil in tests and until wired in production; when
	// either is nil, resolving an AI engine as MT returns PhaseSkipNoProvider
	// instead of translating. Set via SetAIProvider.
	aiTranslator ai.Translator
	aiPredictor  ai.Predictor
	aiDecryptor  Decryptor

	// Dev-only LLM request-response recorder (internal/llmdebug). Nil in
	// production and tests; wired onto the shared HTTPTranslator at the
	// construction sites. Set via SetLLMDebugRecorder.
	llmRec llmdebug.Recorder

	// Extensions run QE+APE for engine rows without a system prompt. Nil
	// until wired (SetExtensions).
	extensions ExtensionPredictors
}

// ExtensionPredictors resolves the extension behind a saved key
// (implemented by *extension.Service).
type ExtensionPredictors interface {
	PredictorForToken(ctx context.Context, tenantID, tokenID int64) (extension.BatchPredictor, error)
}

// SetExtensions wires the extension runtime. Returns s for chaining.
func (s *Service) SetExtensions(e ExtensionPredictors) *Service {
	s.extensions = e
	return s
}

// Decryptor decrypts an at-rest provider secret (implemented by
// *secrets.Encryptor). Kept as a one-method interface so the pipeline doesn't
// import internal/secrets and tests can inject a trivial fake.
type Decryptor interface {
	Decrypt(encoded string) (string, error)
}

// SetAIProvider wires the LLM dependencies: translator (AI-as-MT),
// predictor (LLM QE+APE) and the secret decryptor. Production passes
// the same *ai.HTTPTranslator for both roles; tests that exercise AI engines
// inject fakes (nil for the roles they don't touch). Returns s for chaining.
func (s *Service) SetAIProvider(t ai.Translator, p ai.Predictor, d Decryptor) *Service {
	s.aiTranslator = t
	s.aiPredictor = p
	s.aiDecryptor = d
	return s
}

// SetLLMDebugRecorder wires the dev-only request/response recorder
// (DEV_AUTO_LOGIN gate — see cmd/server/main.go). Returns s for chaining.
func (s *Service) SetLLMDebugRecorder(r llmdebug.Recorder) *Service {
	s.llmRec = r
	return s
}

// NewService constructs a pipeline Service. txr MUST be non-nil — every
// caller that exercises SaveSegmentToTM relies on the tx wrapper for the
// upsert + translation_memory_history atomicity guarantee.
//
// Production: cmd/server/main.go and internal/api/api.go pass
// pipeline.NewPgxTxRunner(pool).
//
// Tests: pass a passthroughTxRunner (defined in mock_querier_test.go) that
// runs the callback against the mockQuerier without opening a real tx.
func NewService(q db.Querier, txr TxRunner, storage FileStorage) *Service {
	return newService(q, txr, storage, mt.NewFromDB)
}

// newService is the internal constructor that lets in-package tests inject
// a custom engine factory (call-counters, fault-injectors). External callers
// go through NewService, which always wires mt.NewFromDB. Keeping this
// unexported means there's no public test-only surface on Service.
func newService(q db.Querier, txr TxRunner, storage FileStorage, engineFactory func(engineType, apiKey, apiURL string, settings map[string]any) (mt.Engine, error)) *Service {
	if txr == nil {
		panic("pipeline.NewService: txr must be non-nil")
	}
	return &Service{
		queries:       q,
		txr:           txr,
		storage:       storage,
		engineFactory: engineFactory,
		scorer:        translationmemory.LevenshteinScorer{},
	}
}

// systemLabelValue returns the text a project carries for a built-in label
// (`customer`, `domain`), which is where those two live since they became
// labels. Empty when the project has no pick or the lookup fails — both mean
// "unset" to every caller.
func (s *Service) systemLabelValue(ctx context.Context, tenantID int64, project db.Project, key string) string {
	v, err := s.queries.GetProjectSystemLabelValue(ctx, db.GetProjectSystemLabelValueParams{
		ProjectID: project.ID, TenantID: tenantID, SystemKey: &key,
	})
	if err != nil {
		return ""
	}
	return v.Value
}

// TranslatePhases controls which phases of the pipeline run when calling
// TranslateFile (TM + MT). The split lets the CAT editor expose "Apply TM" and
// "Apply MT" as independent actions and lets the upload hook respect the two
// settings flags independently. QP+APE is not a pipeline phase: it
// runs as a QE+APE workflow step (see RunAIStep), not on upload.
type TranslatePhases struct {
	UseTM bool
	UseMT bool
	// TranslationIDs limits both phases to these rows of the file — the CAT
	// editor's selection, filtered in the row-list queries. Nil means every
	// row. Each phase still applies its own eligibility rules.
	TranslationIDs []int64 `json:"translation_ids,omitempty"`
}

// FullPipeline runs TM + MT.
var FullPipeline = TranslatePhases{UseTM: true, UseMT: true}

// PhaseSkipReason names why a requested phase was a no-op. The empty value
// means the phase ran (or wasn't requested). Callers use this to surface
// user-facing 400 responses for "missing config" cases vs treating
// "nothing to do" as silent success.
type PhaseSkipReason string

const (
	// PhaseSkipNoAssignment — the project's settings profile has no TM/MT
	// assignment for this language pair. Handler-driven actions (Apply
	// TM/MT buttons) should map this to a 400 with a help message; the
	// upload hook and project pretranslate-all should treat it as a
	// no-op.
	PhaseSkipNoAssignment PhaseSkipReason = "no_assignment"
	// PhaseSkipNoSegments — assignments exist but there's nothing to
	// translate (no targetable segments for TM, no untranslated segments
	// for MT). This is normal success, not a user error.
	PhaseSkipNoSegments PhaseSkipReason = "no_segments"
	// PhaseSkipNoExtensions — the engine runs an extension but this server
	// has no extension runtime wired. Used by the QE+APE step only.
	PhaseSkipNoExtensions PhaseSkipReason = "no_extensions"
	// PhaseSkipNoProvider — the assigned MT engine is an AI engine
	// but its provider isn't connected (no stored/verified credentials), the
	// AI translator isn't wired, or the provider key can't be decrypted.
	// Handler-driven Apply MT should map this to a 400 pointing the user at the
	// AI settings page; async paths treat it as a no-op.
	PhaseSkipNoProvider PhaseSkipReason = "no_provider"
	// PhaseSkipNoRoute — the project has an MT router, but it has no rule for
	// the target language and no default engine. Unlike PhaseSkipNoAssignment
	// (no MT set up at all), the project asked for MT here and got none.
	PhaseSkipNoRoute PhaseSkipReason = "no_route"
)

// PhaseDescription renders a short human-readable label for the active phases.
// Shared by the worker's job-status updates and the synchronous job-handler
// runPhases path so the UI sees consistent step strings either way.
func PhaseDescription(p TranslatePhases) string {
	switch {
	case p.UseTM && p.UseMT:
		return "Running TM lookup + MT"
	case p.UseTM:
		return "Running TM lookup"
	case p.UseMT:
		return "Running MT"
	default:
		return "Pipeline running (no phases enabled)"
	}
}

// IsPipelineRunning is the Go counterpart of the FE helper of the same name.
// `jobs.status='in_progress'` covers two situations:
//
//	(a) workers are actively chewing through segments, OR
//	(b) workers finished but the human hasn't clicked Complete yet.
//
// `current_step` tells them apart: the worker + sync handler write labels
// ending in "complete" (e.g. "Translation complete", "Pipeline complete",
// "tm-apply complete") when a phase finishes. Backend gates
// (HandleClearTranslations, project-wide clear, analysis pre-gate) call this
// so they don't 409 the user on a job whose pipeline genuinely finished but is
// awaiting Complete.
func IsPipelineRunning(status, currentStep string) bool {
	if status != "queued" && status != "in_progress" {
		return false
	}
	return !strings.HasSuffix(strings.ToLower(currentStep), "complete")
}

// TranslateFile runs the requested phases (TM lookup and/or MT) on the
// file's untranslated / TM-targetable segments. The returned TranslateOutcome
// reports per-phase skip reasons so callers can distinguish "config missing"
// (user-actionable) from "nothing to do" (normal success). See
// PhaseSkipReason for the categories. With an MTFailureError the outcome is
// still what the run filled: the finished TM phase and the MT segments saved
// before the failure.
//
// Tenant scoping: the file lookup uses
// GetFileTenant so a stale or cross-tenant fileID returns ErrNoRows
// immediately rather than silently operating on someone else's data.
func (s *Service) TranslateFile(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string, onProgress ProgressFunc, phases TranslatePhases) (TranslateOutcome, error) {
	var outcome TranslateOutcome
	if !phases.UseTM && !phases.UseMT {
		return outcome, nil
	}

	f, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return outcome, fmt.Errorf("get file: %w", err)
	}
	project, err := s.queries.GetProjectByID(ctx, f.ProjectID)
	if err != nil {
		return outcome, fmt.Errorf("get project: %w", err)
	}
	ctx = llmdebug.WithMeta(ctx, llmdebug.Meta{Project: project.Name})
	// TM-scope row: the linked TM profile's settings when the project follows
	// one, else the project's own. Only match scoring is read from it.
	profile, err := s.queries.GetTMScopeSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return outcome, fmt.Errorf("get TM scope settings profile: %w", err)
	}
	fuzzyThreshold := translationmemory.FuzzyThreshold(profile.FuzzyThreshold)

	held := heldSet{}
	if phases.UseTM {
		cfg := tmMatchConfig{
			sourceLang:     sourceLang,
			targetLang:     targetLang,
			fuzzyThreshold: fuzzyThreshold,
			penalties:      translationmemory.PenaltiesFromProfile(profile),
			lookupOrder:    profile.TmLookupOrder,
		}
		filled, names, reason, err := s.runTMPhase(ctx, fileID, project.SettingsProfileID, cfg, phases, held, onProgress)
		if err != nil {
			return outcome, err
		}
		outcome.TMSkipReason, outcome.TMExact, outcome.TMFuzzy, outcome.TMNames = reason, filled.exact, filled.fuzzy, names
	}
	if phases.UseMT {
		run, reason, err := s.runMTPhase(ctx, tenantID, fileID, project, sourceLang, targetLang, phases, held, onProgress)
		outcome.MTSkipReason, outcome.MTTranslated, outcome.MTEngine = reason, run.translated, run.engine
		if err != nil {
			return outcome, &MTFailureError{Err: err}
		}
	}
	outcome.Held = len(held)
	return outcome, nil
}

// runTMPhase operates on the override-eligible set (untranslated /
// pre_translated / predict_pending, no human edits): TM apply upgrades
// MT-filled segments but never touches confirmed or human-edited ones.
//
// Returns what the phase filled and the names of the memories that filled it,
// or the skip reason when it was a no-op (empty when it ran).
// PhaseSkipNoAssignment when the settings profile has no TM assigned for this
// lang pair; PhaseSkipNoSegments when there's nothing to operate on.
func (s *Service) runTMPhase(ctx context.Context, fileID, settingsProfileID int64, cfg tmMatchConfig, phases TranslatePhases, held heldSet, onProgress ProgressFunc) (tmFilled, []string, PhaseSkipReason, error) {
	sourceLang, targetLang := cfg.sourceLang, cfg.targetLang
	tmIDs, assignments := s.resolveAssignedTMs(ctx, settingsProfileID, sourceLang, targetLang)
	if len(tmIDs) == 0 {
		return tmFilled{}, nil, PhaseSkipNoAssignment, nil
	}
	// Per-TM penalties ride along the assignment rows.
	cfg.penalties = cfg.penalties.WithTMs(assignments)

	rows, err := s.queries.ListTMTargetableByFile(ctx, db.ListTMTargetableByFileParams{
		FileID: fileID, TargetLanguage: targetLang, Ids: phases.TranslationIDs, ActorID: TriggeredByFromContext(ctx),
	})
	if err != nil {
		return tmFilled{}, nil, "", fmt.Errorf("list TM-targetable: %w", err)
	}
	var input []db.ListTMTargetableByFileRow
	for _, r := range rows {
		switch {
		case r.HeldByOther:
			held[r.ID] = struct{}{}
		default:
			input = append(input, r)
		}
	}
	if len(input) == 0 {
		return tmFilled{}, nil, PhaseSkipNoSegments, nil
	}
	// Per-segment neighbor-context hashes for 101% promotion. nil on
	// read failure → the phase scores plain 100% (no 101%).
	neighborHashes := s.buildNeighborHashes(ctx, fileID)
	filled, err := s.applyTMMatches(ctx, tmIDs, cfg, input, neighborHashes, onProgress)
	if err != nil {
		return tmFilled{}, nil, "", err
	}
	return filled, filled.names(assignments), "", nil
}

// runMTPhase fills empties only — MT never overrides existing translations.
// Re-fetches AFTER the TM phase so
// TM-filled segments are excluded.
//
// Returns what the phase translated, or the skip reason when it was a no-op
// (empty when it ran). PhaseSkipNoAssignment when no MT router is set up; PhaseSkipNoRoute when the
// router has no engine for this language; PhaseSkipNoSegments when nothing's
// left to translate. Assignment check
// runs first — "MT missing for en→de" is a project-level fact independent
// of whether the file happens to have untranslated segments right now.
func (s *Service) runMTPhase(ctx context.Context, tenantID, fileID int64, project db.Project, sourceLang, targetLang string, phases TranslatePhases, held heldSet, onProgress ProgressFunc) (mtRun, PhaseSkipReason, error) {
	ctx = llmdebug.WithMeta(ctx, llmdebug.Meta{FileID: fileID})
	mtc := &aiMTContext{project: project}
	engine, _, source, attributed, skip, resolveErr := s.resolveMTEngine(ctx, tenantID, project.SettingsProfileID, sourceLang, targetLang, mtc)
	// A router that points at a broken engine is a misconfiguration, not an
	// absence — fail loudly instead of returning a job that looks complete.
	var unusable *EngineUnusableError
	if errors.As(resolveErr, &unusable) {
		return mtRun{}, "", mtUnusable(unusable)
	}
	if engine == nil {
		// Skip MT entirely when no engine is assigned — do NOT fall back to
		// a default: a mock fallback would silently write "[%s] <source>"
		// into every target of a project that lacks an assignment.
		if resolveErr != nil {
			logging.Warn(ctx, resolveErr, "MT phase skipped: engine resolution failed", "pipeline-mt-resolve-failed", append(logging.Langs(sourceLang, targetLang), logging.FileID(fileID))...)
		}
		if skip == "" {
			skip = PhaseSkipNoAssignment
		}
		return mtRun{}, skip, nil
	}

	rows, err := s.queries.ListUntranslatedByFile(ctx, db.ListUntranslatedByFileParams{
		FileID: fileID, TargetLanguage: targetLang, Ids: phases.TranslationIDs, ActorID: TriggeredByFromContext(ctx),
	})
	if err != nil {
		return mtRun{}, "", fmt.Errorf("list untranslated: %w", err)
	}
	var input []db.ListUntranslatedByFileRow
	for _, r := range rows {
		switch {
		case r.HeldByOther:
			held[r.ID] = struct{}{}
		default:
			input = append(input, r)
		}
	}
	if len(input) == 0 {
		return mtRun{}, PhaseSkipNoSegments, nil
	}

	// The engine's own catalog decides the pair before anything is sent. A
	// language it does not list would otherwise leave as a passthrough code and
	// come back as the vendor's own error — or quietly succeed against a code
	// the vendor happens to tolerate, which is worse.
	//
	// Checked here rather than when the router or the project is saved: a
	// rejection here costs a failed phase that was going to fail anyway, while a
	// wrong rejection at save time would block work that works. The catalog
	// answers the same way on every attempt, so the refusal is Permanent.
	if v := mt.ValidateAssignment(ctx, sourceLang, targetLang, engine); v.Rejected {
		logging.Info(ctx, v.Reason, "mt-pair-unsupported",
			logging.FileID(fileID), logging.Engine(engine.Name()),
			logging.SourceLang(sourceLang), logging.TargetLang(targetLang),
			logging.Debugf("engine_row", source))
		return mtRun{}, "", Permanent(httputil.NewClientError(mtUnsupportedMessage(engine.Name(), sourceLang, targetLang)))
	}
	resolvedSourceLang, resolvedTargetLang := mt.ResolveLocalePair(ctx, sourceLang, targetLang, engine)

	saved, err := s.translateMTChunks(ctx, engine, attributed, source, resolvedSourceLang, targetLang, resolvedTargetLang, input, onProgress)
	// A failed run still reports the chunks it saved before the failure.
	run := mtRun{translated: saved, engine: attributed.Name}
	if err != nil {
		// A provider-side failure (bad key, out of quota, rate limited,
		// unreachable) is the user's to fix, not an internal bug. Surface it
		// as an actionable client message instead of an opaque 500.
		var provErr *ai.ProviderError
		if errors.As(err, &provErr) {
			logging.Warn(ctx, err, "MT provider failure", "pipeline-mt-provider-failed", append(logging.Langs(sourceLang, targetLang), logging.Engine(source))...)
			return run, "", httputil.NewClientError(mtProviderMessage(provErr))
		}
		return run, "", err
	}
	return run, "", nil
}

// mtUnsupportedMessage names a pair the engine's catalog does not cover, in the
// same actionable shape as a provider failure. The fix is the router or the
// project's languages, so both are named.
func mtUnsupportedMessage(engineName, sourceLang, targetLang string) string {
	return fmt.Sprintf("Machine translation failed: the %s engine does not translate %s → %s.",
		engineName, sourceLang, targetLang)
}

// mtProviderMessage turns a classified AI-provider failure into a client-safe
// sentence for the Apply MT toast / job error_message.
func mtProviderMessage(e *ai.ProviderError) string {
	return "Machine translation failed: " + providerFailureReason(e)
}

// aiStepProviderMessage is mtProviderMessage for the QE+APE step.
func aiStepProviderMessage(e *ai.ProviderError) string {
	return "The AI step could not complete: " + providerFailureReason(e)
}

// providerFailureReason is the plain-language reason and next step for a
// classified AI-provider failure. The provider's own message text is shown
// neither here nor in the logs, which carry its kind, status and error code.
func providerFailureReason(e *ai.ProviderError) string {
	name := ai.DisplayName(e.Provider)
	switch {
	case e.Kind == ai.ProviderErrAuth:
		return fmt.Sprintf("%s rejected the API key. Check the key in AI settings → Providers.", name)
	case e.Kind == ai.ProviderErrQuota:
		return fmt.Sprintf("The %s account has no credit left. Check your plan and billing.", name)
	case e.Kind == ai.ProviderErrRateLimit:
		return fmt.Sprintf("%s is limiting requests. Wait a minute and try again.", name)
	case e.Kind == ai.ProviderErrUnreachable:
		return fmt.Sprintf("Could not reach %s. Try again in a few minutes.", name)
	case e.Status >= 500:
		return fmt.Sprintf("%s is having a problem right now. Try again in a few minutes.", name)
	default:
		return fmt.Sprintf("%s did not accept the request (error %d). Check the engine settings in AI settings.", name, e.Status)
	}
}

// EngineUnusableError is a router pointing at an engine that cannot translate:
// switched off, or its AI settings key was deleted.
//
// Distinct from the skip reasons on purpose. "No engine assigned" is a
// configuration a user may legitimately have — MT is optional — and skipping is
// right. An engine that IS assigned and cannot run is a broken setup, and
// quietly leaving the segments untranslated buries it: the job reports success
// and somebody finds the gap days later. This fails the job instead, naming the
// engine and the reason.
type EngineUnusableError struct {
	EngineName string
	Reason     enginehealth.Reason
	Page       string // where the engine is edited; set by the step that fails
}

func (e *EngineUnusableError) Error() string { return e.Reason.Message(e.EngineName, e.Page) }

// MTFailureError marks an error from the machine translation step. The message
// is the wrapped error's, unchanged.
type MTFailureError struct{ Err error }

func (e *MTFailureError) Error() string { return e.Err.Error() }
func (e *MTFailureError) Unwrap() error { return e.Err }

// IsMTFailure reports whether err came from the machine translation step.
func IsMTFailure(err error) bool {
	var e *MTFailureError
	return errors.As(err, &e)
}

// mtUnusable and aiStepUnusable put the step in front of the reason and mark
// it Permanent (no retry fixes a missing key); both keep the
// *EngineUnusableError reachable with errors.As.
func mtUnusable(e *EngineUnusableError) error {
	e.Page = enginehealth.PageMT
	return Permanent(fmt.Errorf("Machine translation failed: %w", e))
}

func aiStepUnusable(e *EngineUnusableError) error {
	e.Page = enginehealth.PageAI
	return Permanent(fmt.Errorf("The AI step could not complete: %w", e))
}

// engineRef says WHICH engine produced a translation, in the two mutually
// exclusive forms history records. A vendor engine has an mt_engines row; an AI
// engine has none and is attributed by AIEngineID instead, so the two never mix
// in one dimension.
type engineRef struct {
	MTEngineID int64  // mt_engines.id, or 0 for an AI engine
	AIEngineID int64  // ai_engines.id, or 0 for a vendor engine
	Name       string // the engine row's name, as the organization named it
}

// mtPtr / aiPtr give the nullable FKs history stores; 0 means "not this kind".
func (e engineRef) mtPtr() *int64 {
	if e.MTEngineID == 0 {
		return nil
	}
	id := e.MTEngineID
	return &id
}

func (e engineRef) aiPtr() *int64 {
	if e.AIEngineID == 0 {
		return nil
	}
	id := e.AIEngineID
	return &id
}

// resolveMTEngine returns the engine the profile's MT router picks for the
// target language, or (nil, …, skip, err) when the profile has no router, the
// router has no route for the language, or the engine can't be instantiated.
// Callers MUST treat a nil engine as "skip MT" and use the returned skip
// reason. A route resolves to EITHER a classic MT engine (mt_engines) OR an AI
// engine of purpose 'mt'; a language rule beats the router's default engine
// (see mtrouter.ResolveEngine).
//
// The engineRef return says which engine produced the translation, for
// EmitAutomationHistory to record on translation_history: a vendor
// engine as mt_engine_id, an AI engine as ai_engine_id. MTPreview and other
// read-only callers can discard it.
//
// mtc carries the job-level context an AI engine needs on top of the language
// pair (the project behind the segment prompt). Nil is valid and
// means "no segment prompt" — what one-shot callers like MTPreview want, and
// irrelevant to classic engines.
func (s *Service) resolveMTEngine(ctx context.Context, tenantID, settingsProfileID int64, sourceLang, targetLang string, mtc *aiMTContext) (engine mt.Engine, apiURL, source string, attributed engineRef, skip PhaseSkipReason, err error) {
	profile, lookupErr := s.queries.GetSettingsProfile(ctx, settingsProfileID)
	if lookupErr != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("get settings profile %d: %w", settingsProfileID, lookupErr)
	}
	if profile.MtRouterID == nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("no MT router on settings profile %d", settingsProfileID)
	}
	// The router picks the engine by the job's target language (rule, else
	// default); the source language plays no part.
	ref, lookupErr := mtrouter.ResolveEngine(ctx, s.queries, tenantID, *profile.MtRouterID, targetLang)
	if lookupErr != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("resolve MT router %d: %w", *profile.MtRouterID, lookupErr)
	}
	row := ref

	if row.MTEngineID != nil {
		eng, getErr := s.queries.GetMTEngineCredential(ctx, db.GetMTEngineCredentialParams{ID: *row.MTEngineID, TenantID: tenantID})
		if getErr != nil {
			return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("load MT engine %d: %w", *row.MTEngineID, getErr)
		}
		// Switched off, or the key it spent was deleted. Either way the router
		// routes to something that cannot run, which fails the job rather than
		// silently leaving the segments alone.
		if reason := enginehealth.Of(eng.Enabled, mtengine.NeedsToken(eng.EngineType), eng.TokenID != nil); !reason.Usable() {
			return nil, "", "", engineRef{}, "", &EngineUnusableError{EngineName: eng.Name, Reason: reason}
		}
		apiKey, apiURL, credErr := mtengine.Credential(eng, s.aiDecryptor)
		if credErr != nil {
			return nil, "", "", engineRef{}, PhaseSkipNoProvider, fmt.Errorf("credential for MT engine %d: %w", eng.ID, credErr)
		}
		settings, settingsErr := decodeEngineSettings(eng.Settings)
		if settingsErr != nil {
			return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("settings for MT engine %d: %w", eng.ID, settingsErr)
		}
		resolved, factoryErr := s.engineFactory(eng.EngineType, apiKey, apiURL, settings)
		if factoryErr != nil {
			return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("create engine %s (id=%d): %w", eng.EngineType, eng.ID, factoryErr)
		}
		return resolved, apiURL, fmt.Sprintf("%s (id=%d, url=%s)", eng.EngineType, eng.ID, apiURL), engineRef{MTEngineID: eng.ID, Name: eng.Name}, "", nil
	}

	if row.AIEngineID != nil {
		return s.resolveAIMTEngine(ctx, tenantID, *row.AIEngineID, sourceLang, targetLang, mtc)
	}
	return nil, "", "", engineRef{}, PhaseSkipNoRoute, fmt.Errorf("MT router %d has no engine for %q", *profile.MtRouterID, targetLang)
}

// matchAIEngineRow picks the engine row covering source→target. Rows may use
// langcode.Wildcard ("*") for their source and/or targets to mean "any
// language". The most specific match wins: a specific source scores over a
// wildcard source, and a specific target over a wildcard target. Ties resolve to
// the first row (lowest sort order). Returns nil when no row matches.
func matchAIEngineRow(rows []db.AiEngineRow, sourceLang, targetLang string) *db.AiEngineRow {
	wantSource := langcode.Normalize(sourceLang)
	wantTarget := langcode.Normalize(targetLang)
	var matched *db.AiEngineRow
	bestScore := -1
	for i := range rows {
		srcWild := strings.TrimSpace(rows[i].SourceLang) == langcode.Wildcard
		if !srcWild && langcode.Normalize(rows[i].SourceLang) != wantSource {
			continue
		}
		tgtWild, tgtExact := false, false
		for _, tl := range rows[i].TargetLangs {
			if strings.TrimSpace(tl) == langcode.Wildcard {
				tgtWild = true
			} else if langcode.Normalize(tl) == wantTarget {
				tgtExact = true
			}
		}
		if !tgtExact && !tgtWild {
			continue
		}
		score := 0
		if !srcWild {
			score += 2
		}
		if tgtExact {
			score++
		}
		if score > bestScore {
			bestScore = score
			matched = &rows[i]
		}
	}
	return matched
}

// aiMTContext is the job-level context an AI MT engine needs beyond the
// language pair: the project behind the segment prompt's
// placeholders. Classic MT engines ignore it; nil means the system falls back
// to sending the bare source text per segment.
type aiMTContext struct {
	project db.Project
}

// resolveAIMTEngine builds an mt.Engine backed by an AI provider from the
// engine's row matching this source→target pair: the
// row's system prompt supplies the provider, model, and instructions. Returns
// PhaseSkipNoProvider (not NoAssignment) when the AI path is unavailable — the
// assignment exists, the user just needs to connect the provider / wire the
// translator — so handlers can surface a provider-specific message instead of
// "no engine assigned".
func (s *Service) resolveAIMTEngine(ctx context.Context, tenantID, aiEngineID int64, sourceLang, targetLang string, mtc *aiMTContext) (mt.Engine, string, string, engineRef, PhaseSkipReason, error) {
	if s.aiTranslator == nil || s.aiDecryptor == nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider, fmt.Errorf("AI translation is not configured on this server")
	}
	engine, err := s.queries.GetAIEngine(ctx, db.GetAIEngineParams{ID: aiEngineID, TenantID: tenantID})
	if err != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("load AI engine %d: %w", aiEngineID, err)
	}
	// Switched off, same as an MT engine: the router points at something that
	// cannot run, which fails the job rather than quietly skipping.
	if !engine.Enabled {
		return nil, "", "", engineRef{}, "", &EngineUnusableError{EngineName: engine.Name, Reason: enginehealth.Disabled}
	}
	// Only mt-purpose engines translate. The assignment save path
	// validates this too; the guard catches older rows saved before that check.
	if engine.Purpose != aiengine.PurposeMT {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider,
			fmt.Errorf("AI engine %q is a QE+APE engine — not usable as MT", engine.Name)
	}
	// Row model: the engine routes each source→target pair
	// to a system prompt. Pick the row covering this pair; its prompt carries
	// the provider/model/instructions that drive the LLM.
	rows, err := s.queries.ListAIEngineRows(ctx, aiEngineID)
	if err != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("list rows for AI engine %d: %w", aiEngineID, err)
	}
	matched := matchAIEngineRow(rows, sourceLang, targetLang)
	if matched == nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider,
			fmt.Errorf("AI engine %d has no row for %s→%s", aiEngineID, sourceLang, targetLang)
	}
	// The key this row ran with was deleted from AI settings. A row's token is
	// required at save, so a null one can only mean that.
	if matched.TokenID == nil {
		return nil, "", "", engineRef{}, "", &EngineUnusableError{EngineName: engine.Name, Reason: enginehealth.NoKey}
	}
	// A row without a system prompt runs an extension, which reviews
	// translations and cannot produce them. Skip cleanly.
	if matched.SystemPromptID == nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider,
			fmt.Errorf("AI engine %d row for %s→%s runs an extension (not usable as MT)", aiEngineID, sourceLang, targetLang)
	}
	prompt, err := s.queries.GetAIPrompt(ctx, db.GetAIPromptParams{ID: *matched.SystemPromptID, TenantID: tenantID})
	if err != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoAssignment, fmt.Errorf("load system prompt %d: %w", *matched.SystemPromptID, err)
	}
	// Token + model live on the row itself; the vendor comes
	// from the token.
	if matched.TokenID == nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider, fmt.Errorf("AI engine %d row for %s→%s has no model configured", aiEngineID, sourceLang, targetLang)
	}
	tok, err := s.queries.GetVendorToken(ctx, db.GetVendorTokenParams{ID: *matched.TokenID, TenantID: tenantID})
	if err != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider, fmt.Errorf("load token %d: %w", *matched.TokenID, err)
	}
	provider := strings.TrimSpace(tok.Provider)
	model := strings.TrimSpace(matched.Model)
	apiKey, err := s.aiDecryptor.Decrypt(tok.ApiKeyEncrypted)
	if err != nil {
		return nil, "", "", engineRef{}, PhaseSkipNoProvider, fmt.Errorf("decrypt provider %q key: %w", provider, err)
	}
	baseURL := ""
	baseURL = strings.TrimSpace(tok.BaseUrl)
	promptText := prompt.PromptText
	// Fetched once per run; applied per request below, because an MT engine
	// serves several target languages and each batch carries its own pair.
	langAdditions := s.loadLanguageAdditions(ctx, prompt.ID)
	// The row's model settings ride along on every request;
	// resolved once, like the credentials.
	settings := ai.ParseSettings(ctx, matched.Settings)

	// Each segment's user message is generated from the context the system
	// prompt asks for. Built here (once per run) rather than per chunk so any
	// TM assignment is resolved a single time.
	var renderer *segPromptRenderer
	if mtc != nil {
		renderer = s.newSegPromptRenderer(ctx, tenantID, prompt, aiprompt.Mode(prompt), mtc.project, targetLang)
	}
	// Without a project the tokens are recorded but cannot be charted.
	var usageProjectID int64
	if mtc != nil {
		usageProjectID = mtc.project.ID
	}
	fn := func(ctx context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
		ctx = llmdebug.WithMeta(ctx, llmdebug.Meta{Engine: engine.Name})
		// The job, when there is one, is set on the debug meta by the worker.
		ctx = aiusage.WithMeta(ctx, aiusage.Meta{
			TenantID: tenantID, AIEngineID: engine.ID, AIEngineName: engine.Name, Purpose: engine.Purpose,
			ProjectID: usageProjectID, JobID: llmdebug.MetaFromContext(ctx).JobID,
		})
		cfg := ai.TranslateConfig{
			Provider:   provider,
			Model:      model,
			APIKey:     apiKey,
			BaseURL:    baseURL,
			PromptText: applyLanguageAdditions(promptText, langAdditions, req.TargetLang),
			SourceLang: req.SourceLang,
			TargetLang: req.TargetLang,
			// The contract describes the block the segments actually carry.
			SegmentContext: prompt.SegmentContext,
			Settings:       settings,
			// Set by LLMEngine.TranslateTagged for the rich sub-batch, so the
			// tag contract rides only on requests that actually carry chips.
			Tagged: req.Tagged,
		}
		rows := make([]ai.TranslateInput, len(req.Texts))
		for i, text := range req.Texts {
			rows[i] = ai.TranslateInput{ID: strconv.Itoa(i), Text: text}
		}
		// Segment identity is optional: without it the context that needs only
		// the source text (the TM match) still resolves, and the ids stay
		// positional.
		if renderer != nil {
			identified := len(req.Rows) == len(req.Texts)
			segRows := make([]segPromptRow, len(req.Texts))
			for i, text := range req.Texts {
				segRows[i] = segPromptRow{SourceText: text}
				if identified {
					segRows[i].TranslationID, segRows[i].PlainSource = req.Rows[i].TranslationID, req.Rows[i].PlainSource
					rows[i].ID = strconv.FormatInt(req.Rows[i].TranslationID, 10)
				}
			}
			for i, content := range renderer.RenderBatch(ctx, segRows) {
				rows[i].Content = content
			}
		}

		results, err := s.aiTranslator.TranslateBatch(ctx, cfg, rows)
		if err != nil {
			return nil, err
		}
		out := make([]mt.TranslateResult, len(results))
		for i, r := range results {
			out[i] = mt.TranslateResult{Text: r.Text}
		}
		return out, nil
	}
	llm := mt.LLMEngine{EngineName: "ai:" + engine.Name, Fn: fn}
	// Attributed by ai_engine_id, not mt_engine_id: an AI engine has no
	// mt_engines row, and the two are charted separately.
	return llm, baseURL, fmt.Sprintf("ai-mt %q (provider=%s, model=%s)", engine.Name, provider, model), engineRef{AIEngineID: engine.ID, Name: engine.Name}, "", nil
}

// translateMTChunks calls the engine in batches of mtChunkSize, saving each
// chunk's results before moving on, and returns how many segments it saved.
// Cancellation is checked at chunk boundaries so prior chunks remain persisted
// on abort (cooperative cancellation when a job is deleted mid-flight).
func (s *Service) translateMTChunks(ctx context.Context, engine mt.Engine, attributed engineRef, engineSource, sourceLang, targetLang, resolvedTargetLang string, input []db.ListUntranslatedByFileRow, onProgress ProgressFunc) (int, error) {
	engineName := engine.Name()
	total := len(input)
	translated, saved := 0, 0

	// When the engine handles inline tags natively, tagged
	// segments are sent as rich text and come back with tags already placed —
	// no TransferTags projection. Detected once via type assertion.
	tagEngine, tagAware := engine.(mt.TagPreservingEngine)

	for i := 0; i < total; i += mtChunkSize {
		if ctx.Err() != nil {
			return saved, fmt.Errorf("mt translate aborted (translated %d/%d): %w", translated, total, ctx.Err())
		}
		end := i + mtChunkSize
		if end > total {
			end = total
		}
		chunk := input[i:end]

		// One span per chunk — n ends up as the number of engine round trips
		// (segments / mtChunkSize), which is the unit that actually costs
		// latency. Tagged by engine so a job re-run on a different provider
		// stays attributable.
		chunkCtx, doneChunk := timing.Start(ctx, timing.StageMTEngineCall,
			timing.Meta("engine", engineSource), timing.Meta("tag_aware", strconv.FormatBool(tagAware)))
		results, tagged, err := s.translateChunk(chunkCtx, engine, tagEngine, sourceLang, targetLang, resolvedTargetLang, chunk, i+1, end, total)
		doneChunk(err)
		if err != nil {
			return saved, fmt.Errorf("mt translate (translated %d/%d before failure): %w", translated, total, err)
		}

		// The engine round trip above is already paid for; a cancellation
		// landing between it and the save must not drop the chunk
		// (saveMTChunk logs-and-continues on write errors, so the loss would
		// be silent). Detached via WithoutCancel, bounded so a hung
		// DB can't wedge shutdown. 30s, not tighter: a tag-heavy chunk is up
		// to ~90 writes plus TransferTags CPU, and an expired bound here
		// silently drops the remaining rows while the handler reports success.
		func() {
			persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancelPersist()
			// saveCtx, not ctx: mt.tag_transfer starts inside saveMTChunk and
			// must land as a CHILD of this span so its time is subtracted from
			// db_save's self rather than counted under both.
			saveCtx, doneSave := timing.Start(persistCtx, timing.StageMTDBSave)
			saved += s.saveMTChunk(saveCtx, chunk, results, tagged, engineName, attributed)
			doneSave(nil)
		}()

		translated += len(chunk)
		if onProgress != nil {
			onProgress("mt", translated, total)
		}
	}
	return saved, nil
}

// translateChunk translates one chunk. When tagEngine is non-nil it
// partitions the chunk into rows that carry source_rich_text and
// rows that don't: the former are sent as rich text through TranslateTagged
// (engine repositions the tags), the latter through plain Translate. Plain
// text is never sent under XML tag mode — a stray `<` in body text would be
// mis-parsed as a tag. Results are reassembled in the chunk's original order;
// the parallel `tagged` slice marks which results are engine-native rich text.
func (s *Service) translateChunk(ctx context.Context, engine mt.Engine, tagEngine mt.TagPreservingEngine, sourceLang, targetLang, resolvedTargetLang string, chunk []db.ListUntranslatedByFileRow, chunkStart, chunkEnd, total int) ([]mt.TranslateResult, []bool, error) {
	results := make([]mt.TranslateResult, len(chunk))
	tagged := make([]bool, len(chunk))

	// Partition by "has rich text AND engine is tag-aware".
	var richIdx, plainIdx []int
	for j, row := range chunk {
		if tagEngine != nil && row.SourceRichText != nil && *row.SourceRichText != "" {
			richIdx = append(richIdx, j)
		} else {
			plainIdx = append(plainIdx, j)
		}
	}

	// Plain sub-batch — stripped source text through the standard path.
	if len(plainIdx) > 0 {
		texts := make([]string, len(plainIdx))
		// Rows lets an AI engine render each segment's prompt;
		// classic engines ignore it. Index-aligned with texts, so the sub-batch
		// keeps its document order.
		refs := make([]mt.SegmentRef, len(plainIdx))
		for k, j := range plainIdx {
			texts[k] = chunk[j].SourceText
			refs[k] = mt.SegmentRef{TranslationID: chunk[j].ID, SegmentID: chunk[j].SegmentID, PlainSource: chunk[j].SourceText}
		}
		res, err := engine.Translate(ctx, mt.TranslateRequest{SourceLang: sourceLang, TargetLang: resolvedTargetLang, Texts: texts, Rows: refs})
		if err != nil {
			return nil, nil, s.chunkErr(ctx, "plain", chunkStart, chunkEnd, total, engine.Name(), sourceLang, targetLang, err)
		}
		if len(res) != len(plainIdx) {
			return nil, nil, fmt.Errorf("mt returned %d results for %d plain texts", len(res), len(plainIdx))
		}
		for k, j := range plainIdx {
			results[j] = res[k]
		}
	}

	// Rich sub-batch — source rich text through the tag-preserving path.
	if len(richIdx) > 0 {
		texts := make([]string, len(richIdx))
		refs := make([]mt.SegmentRef, len(richIdx))
		for k, j := range richIdx {
			texts[k] = *chunk[j].SourceRichText
			refs[k] = mt.SegmentRef{TranslationID: chunk[j].ID, SegmentID: chunk[j].SegmentID, PlainSource: chunk[j].SourceText}
		}
		res, err := tagEngine.TranslateTagged(ctx, mt.TranslateRequest{SourceLang: sourceLang, TargetLang: resolvedTargetLang, Texts: texts, Rows: refs})
		if err != nil {
			return nil, nil, s.chunkErr(ctx, "tagged", chunkStart, chunkEnd, total, engine.Name(), sourceLang, targetLang, err)
		}
		if len(res) != len(richIdx) {
			return nil, nil, fmt.Errorf("mt returned %d results for %d tagged texts", len(res), len(richIdx))
		}
		for k, j := range richIdx {
			results[j] = res[k]
			tagged[j] = true
		}
	}

	return results, tagged, nil
}

// chunkErr formats a chunk-translation error with abort vs failure context.
func (s *Service) chunkErr(ctx context.Context, path string, chunkStart, chunkEnd, total int, engineName, sourceLang, targetLang string, err error) error {
	if ctx.Err() != nil {
		logging.Warn(ctx, ctx.Err(), "MT chunk aborted: context cancelled", "pipeline-mt-chunk-aborted", chunkAttrs(path, chunkStart, chunkEnd, total, engineName, sourceLang, targetLang)...)
	} else {
		logging.Error(ctx, err, "MT chunk failed", "pipeline-mt-chunk-failed", chunkAttrs(path, chunkStart, chunkEnd, total, engineName, sourceLang, targetLang)...)
	}
	return err
}

// chunkAttrs describes one MT chunk for a log line.
func chunkAttrs(path string, chunkStart, chunkEnd, total int, engineName, sourceLang, targetLang string) []slog.Attr {
	return append(logging.Langs(sourceLang, targetLang),
		logging.Engine(engineName),
		logging.Label("mt_path", path),
		logging.Label("chunk", fmt.Sprintf("%d-%d/%d", chunkStart, chunkEnd, total)))
}

// saveMTChunk writes a chunk's results and returns how many rows it wrote:
// per-row write failures are logged and skipped — one bad row shouldn't lose
// the chunk's other results.
//
// After each successful MT-fill, emit an `mt` event into
// translation_history and update the denormalized lineage on translations
// (origin='mt', mt_match_percent left NULL). The emission is best-effort —
// canonical MT-fill writes are not gated on its success.
//
// `tagged[j]` marks rows whose result is engine-native rich text
// (Tier 2 — the engine repositioned the inline tags). For those, the rich
// text comes straight from the engine and active_text is the tag-stripped
// plain form. For tagged=false rows that still carry source_rich_text, we
// project tags onto the plain MT output via TransferTags (Tier 1). Plain rows
// store plain text only.
func (s *Service) saveMTChunk(ctx context.Context, chunk []db.ListUntranslatedByFileRow, results []mt.TranslateResult, tagged []bool, engineName string, attributed engineRef) int {
	filled := make([]int64, 0, len(chunk))
	for j, row := range chunk {
		out := results[j].Text
		// A batched reply can omit a segment (and its split-retry can
		// too). Writing the empty result would mark the row translated with
		// blank text — worse than leaving it for the next MT run or the human.
		if strings.TrimSpace(out) == "" {
			logging.Warn(ctx, nil, "MT engine returned no text; segment left untranslated", "pipeline-mt-empty-result", logging.TranslationID(row.ID))
			continue
		}
		var activeText string
		var activeRichText *string

		hasSourceTags := row.SourceRichText != nil && *row.SourceRichText != ""
		switch {
		case tagged[j]:
			// Tier 2: engine returned rich text with tags already placed.
			activeText = stripInlineTags(out)
			richCopy := out
			activeRichText = &richCopy
		case hasSourceTags:
			// Tier 1: project source tags onto the plain MT output. Nested
			// inside the mt.db_save span on purpose — this is the CPU slice
			// of that stage, broken out because tag-heavy DOCX makes it the
			// dominant cost while "db_save" suggests it's all I/O.
			activeText = out
			_, doneTags := timing.Start(ctx, timing.StageMTTagTransfer)
			projected := TransferTags(*row.SourceRichText, row.SourceText, out)
			doneTags(nil)
			if projected != "" {
				activeRichText = &projected
			}
		default:
			// No tags either way — plain text.
			activeText = out
		}

		if _, err := s.queries.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{
			ID:             row.ID,
			ActiveText:     &activeText,
			ActiveRichText: activeRichText,
			ActorID:        TriggeredByFromContext(ctx),
		}); err != nil {
			// Pgx.ErrNoRows means the row's eligibility changed
			// between ListUntranslatedByFile and this write — typically a
			// concurrent human confirm in another tab, or another user
			// opening the segment. Skip silently (the human's content wins)
			// instead of clobbering via a retry.
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			logging.Error(ctx, err, "failed to save MT result", "pipeline-mt-save-failed", logging.TranslationID(row.ID))
			continue
		}
		filled = append(filled, row.ID)
		// Exactly one of these is set: an AI engine has no mt_engines row, and a
		// vendor engine has no ai_engines row. History keeps them in separate
		// columns so the two never mix in one engine dimension.
		mtEngineIDPtr, aiEngineIDPtr := attributed.mtPtr(), attributed.aiPtr()
		s.EmitAutomationHistory(ctx, AutomationEmitOpts{
			TranslationID: row.ID,
			Kind:          db.TranslationHistoryKindMt,
			Origin:        db.TranslationOriginMt,
			Text:          &activeText,
			RichText:      activeRichText,
			CreatedBy:     TriggeredByFromContext(ctx),
			MtEngineID:    mtEngineIDPtr,
			AIEngineID:    aiEngineIDPtr,
			Details:       map[string]any{"engine_name": engineName},
		})
	}
	// Runs on this function's detached, bounded context, like the writes above:
	// a cancellation between the engine round trip and here must not leave the
	// saved rows carrying findings from their pre-MT text.
	s.dropStaleQAResults(ctx, filled)
	return len(filled)
}

// MTPreviewOutput is one live MT suggestion and the engine that produced it.
type MTPreviewOutput struct {
	Text, RichText, EngineName string
	// MTEngineID / AIEngineID: the engine, as translation_history attributes
	// it. At most one is set.
	MTEngineID, AIEngineID *int64
}

// MTPreview returns a one-shot machine translation for the CAT editor
// sidebar — fetched live and never stored. Only reached when the
// segment has no stored MT: HandleMTPreview serves translation_history
// kind='mt' first, so with an
// AI-as-MT engine this paid LLM call fires only for never-translated
// segments. When the project has no MT engine assigned for this lang pair,
// returns an empty output and no error.
// sourceRichText is the segment's tagged source, empty when it has no inline
// tags. When it is present and the engine handles tags, the preview goes
// through the tag-preserving path and returns rich text as well — the sidebar
// shows chips and inserting keeps the tags, matching what the pipeline stores.
// translationID identifies the segment (0 when there is none), so an AI
// engine's prompt gets the same per-segment context as a pipeline run.
func (s *Service) MTPreview(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang, sourceText, sourceRichText string, translationID int64) (MTPreviewOutput, error) {
	f, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return MTPreviewOutput{}, fmt.Errorf("get file: %w", err)
	}
	project, err := s.queries.GetProjectByID(ctx, f.ProjectID)
	if err != nil {
		return MTPreviewOutput{}, fmt.Errorf("get project: %w", err)
	}
	ctx = llmdebug.WithMeta(ctx, llmdebug.Meta{FileID: fileID, Project: project.Name})

	engine, _, _, attributed, _, _ := s.resolveMTEngine(ctx, tenantID, project.SettingsProfileID, sourceLang, targetLang, &aiMTContext{project: project})
	if engine == nil {
		return MTPreviewOutput{}, nil
	}
	out := MTPreviewOutput{EngineName: engine.Name(), MTEngineID: attributed.mtPtr(), AIEngineID: attributed.aiPtr()}
	// Same catalog check the MT phase makes, so the sidebar explains a pair the
	// engine cannot take instead of surfacing the vendor's wording.
	if v := mt.ValidateAssignment(ctx, sourceLang, targetLang, engine); v.Rejected {
		return out, httputil.NewClientError(mtUnsupportedMessage(engine.Name(), sourceLang, targetLang))
	}
	resolvedSourceLang, resolvedTargetLang := mt.ResolveLocalePair(ctx, sourceLang, targetLang, engine)
	req := mt.TranslateRequest{
		SourceLang: resolvedSourceLang,
		TargetLang: resolvedTargetLang,
		Texts:      []string{sourceText},
		Rows:       []mt.SegmentRef{{TranslationID: translationID, PlainSource: sourceText}},
	}

	tagEngine, tagAware := engine.(mt.TagPreservingEngine)
	tagged := tagAware && sourceRichText != ""
	var results []mt.TranslateResult
	if tagged {
		req.Texts = []string{sourceRichText}
		results, err = tagEngine.TranslateTagged(ctx, req)
	} else {
		results, err = engine.Translate(ctx, req)
	}
	if err != nil {
		return out, fmt.Errorf("mt: %w", err)
	}
	if len(results) == 0 {
		return out, nil
	}
	if tagged {
		out.Text, out.RichText = stripInlineTags(results[0].Text), results[0].Text
		return out, nil
	}
	out.Text = results[0].Text
	return out, nil
}

// applyTMMatches runs exact then (optionally) fuzzy TM lookup. Exact matches
// auto-approve via UpdateTranslationHuman (historical "100% = confirmed");
// fuzzy matches only set the metadata. Lookup errors degrade silently so the
// caller can still run MT.
//
// cfg.lookupOrder picks the walk: match_percent runs the
// exact pass over every TM and then the fuzzy pass over every TM; tm_order
// runs both passes for one TM before moving to the next. Either way `matched`
// is shared, so a segment filled once is never filled again.
func (s *Service) applyTMMatches(
	ctx context.Context,
	tmIDs []int64,
	cfg tmMatchConfig,
	tmInput []db.ListTMTargetableByFileRow,
	neighborHashes map[int64]translationmemory.NeighborContext,
	onProgress ProgressFunc,
) (tmFilled, error) {
	matched := make(map[int64]bool, len(tmInput))
	passes := [][]int64{tmIDs}
	if cfg.lookupOrder == settings.TMLookupOrderTMOrder {
		passes = make([][]int64, len(tmIDs))
		for i, id := range tmIDs {
			passes[i] = []int64{id}
		}
	}
	var filled tmFilled
	for _, pass := range passes {
		filled = filled.add(s.applyExactTMMatches(ctx, pass, cfg, tmInput, neighborHashes, matched, filled.total(), onProgress))
		if cfg.fuzzyThreshold < 1.0 {
			filled = filled.add(s.applyFuzzyTMMatches(ctx, pass, cfg, tmInput, matched, filled.total(), onProgress))
		}
	}

	if total := filled.total(); onProgress != nil && total > 0 {
		onProgress("tm", total, len(tmInput))
	}
	// `matched` holds every row both sub-phases filled — exactly the rows whose
	// target text changed, and so exactly the rows whose findings are stale.
	rewritten := make([]int64, 0, len(matched))
	for id := range matched {
		rewritten = append(rewritten, id)
	}
	s.dropStaleQAResults(ctx, rewritten)
	return filled, nil
}

// dropStaleQAResults deletes the stored QA findings of rows the pipeline just
// rewrote. A finding describes one exact target text, and its ignore flag is
// fingerprinted on the message that text produced — neither
// says anything about the text that replaced it.
//
// Best-effort, like the history emissions around it: the write it follows has
// already landed, and failing the phase would not undo it. A retry re-runs the
// phase against rows that are now filled, so it would not reach this delete
// again — hence the loud log rather than a returned error.
func (s *Service) dropStaleQAResults(ctx context.Context, translationIDs []int64) {
	if len(translationIDs) == 0 {
		return
	}
	if err := s.queries.DeleteQAResultsForTranslations(ctx, translationIDs); err != nil {
		logging.Warn(ctx, err, "failed to drop stale QA findings for pre-translated segments",
			"pipeline-qa-stale-drop", slog.Int("count", len(translationIDs)))
	}
}

// Returns the TM ids in priority order plus the raw assignment rows, which
// carry the per-TM penalty the caller folds into its Penalties.
func (s *Service) resolveAssignedTMs(ctx context.Context, settingsProfileID int64, sourceLang, targetLang string) ([]int64, []db.LookupSettingsTMsRow) {
	rows, err := s.queries.LookupSettingsTMs(ctx, db.LookupSettingsTMsParams{
		SettingsProfileID: settingsProfileID,
		SourceLanguage:    sourceLang,
		TargetLanguage:    targetLang,
	})
	if err != nil {
		logging.Warn(ctx, err, "TM assignment lookup failed", "pipeline-tm-assignments-failed", append(logging.Langs(sourceLang, targetLang), logging.SettingsProfileID(settingsProfileID))...)
		return nil, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.TmID)
	}
	return ids, rows
}

// applyExactTMMatches runs batched exact lookups per TM in priority order.
// `matched` (mutated) dedupes across TMs so a lower-priority TM can't
// overwrite a higher-priority hit. `progressBase` keeps the reported
// total monotonic when the phase calls this once per TM. A byte-identical hit
// that pays a penalty counts as fuzzy: it is left for review.
func (s *Service) applyExactTMMatches(
	ctx context.Context,
	tmIDs []int64,
	cfg tmMatchConfig,
	tmInput []db.ListTMTargetableByFileRow,
	neighborHashes map[int64]translationmemory.NeighborContext,
	matched map[int64]bool,
	progressBase int,
	onProgress ProgressFunc,
) tmFilled {
	sourceLang, targetLang := cfg.sourceLang, cfg.targetLang
	sourceTexts := make([]string, len(tmInput))
	for i, row := range tmInput {
		sourceTexts[i] = row.SourceText
	}
	var filled tmFilled
	for _, tmID := range tmIDs {
		for start := 0; start < len(sourceTexts); start += tmExactBatchSize {
			end := min(start+tmExactBatchSize, len(sourceTexts))
			// n counts batched lookups issued (one per TM per 500 sources),
			// not segments — see internal/timing/stages.go.
			lookupCtx, doneLookup := timing.Start(ctx, timing.StageTMExactLookup)
			hits, err := s.queries.LookupTMBatch(lookupCtx, db.LookupTMBatchParams{
				TmID:           tmID,
				SourceLanguage: sourceLang,
				TargetLanguage: targetLang,
				SourceTexts:    sourceTexts[start:end],
			})
			doneLookup(err)
			if err != nil {
				logging.Warn(ctx, err, "TM batch lookup failed", "pipeline-tm-batch-lookup-failed", logging.TMID(tmID), logging.Label("batch", fmt.Sprintf("%d-%d", start, end)))
				continue
			}
			hitBySource := firstHitBySource(hits)
			for i := start; i < end; i++ {
				row := tmInput[i]
				if matched[row.ID] {
					continue
				}
				hit, ok := hitBySource[row.SourceText]
				if !ok || hit.TargetText == "" {
					continue
				}
				// A penalised hit is never a true exact match, even when the
				// source text is byte-identical: it forfeits the in-context
				// bonus and is left for a human instead of being auto-confirmed.
				// Deduction 0 — no locale mismatch, no per-TM penalty — is what
				// earns the 100/101 and the auto-confirm.
				deduction := cfg.penalties.Deduction(translationmemory.MatchInfo{
					TMID:    tmID,
					WantSrc: sourceLang, GotSrc: hit.SourceLanguage,
					WantTgt: targetLang, GotTgt: hit.TargetLanguage,
				})
				exact := deduction == 0
				// 101% when the hit's stored neighbor context matches
				// this segment's neighbors, else plain 100%.
				percent := translationmemory.ScoreToPercent(1.0 - deduction)
				if exact {
					var segment *translationmemory.NeighborContext
					if nc, ok := neighborHashes[row.SegmentID]; ok {
						segment = &nc
					}
					percent = translationmemory.ExactPercent(hit.PrevSourceHash, hit.NextSourceHash, segment)
				}
				// Re-anchor the source segment's tags onto the
				// TM target so a tagged segment doesn't land tag-free.
				rich := tmTargetRichText(
					ptr.Deref(row.SourceRichText), row.SourceText,
					hit.TargetText, ptr.Deref(hit.TargetRichText))
				if s.applyTMFill(ctx, row.ID, tmID, hit.ID, hit.TargetText, rich, percent, exact) {
					matched[row.ID] = true
					filled.record(tmID, exact)
				}
			}
			if onProgress != nil {
				onProgress("tm", progressBase+filled.total(), len(tmInput))
			}
		}
	}
	return filled
}

// firstHitBySource: LookupTMBatch orders by best-locale-first (target locale
// outranking source locale) then id, so the first row per source_text is the
// best hit under the sublanguage fallback rule.
func firstHitBySource(hits []db.LookupTMBatchRow) map[string]db.LookupTMBatchRow {
	out := make(map[string]db.LookupTMBatchRow, len(hits))
	for _, h := range hits {
		if _, exists := out[h.SourceText]; !exists {
			out[h.SourceText] = h
		}
	}
	return out
}

// applyTMFill writes the TM metadata then auto-approves via
// UpdateTranslationHuman. Deliberately not in a tx: a partial failure leaves
// the row TM-targetable on retry, so the system self-heals.
//
// Also emits a `tm` event into translation_history and updates
// the denormalized lineage (translations.origin='tm', tm_match_percent) via
// the EmitAutomationHistory helper. `percent` is 100 for a plain exact match,
// 101 for an in-context exact match, or the rescored fuzzy percentage.
// The emission is best-effort — canonical TM-fill writes are not gated on its
// success. The auto-approval via UpdateTranslationHuman is treated as part of
// the same conceptual event (no separate 'human' history row — exact-match
// auto-approval is a pipeline action, not a user action).
//
// autoConfirm is true only for a true exact match — same source text AND the
// same locale on both sides. A sublanguage variant (an `en` entry matched on an
// `en-us` project) is filled but left for a human, since nobody asked us to
// approve a translation written for a different locale.
func (s *Service) applyTMFill(ctx context.Context, translationID, tmID, tmEntryID int64, targetText, targetRichText string, percent int16, autoConfirm bool) bool {
	// n = matches actually written to translations (both the fill and its
	// auto-approval), the number that pairs with tm.exact_lookup's query count.
	ctx, done := timing.Start(ctx, timing.StageTMApply)
	defer func() { done(nil) }()
	// The fill and the confirm pass the same user's holds, so a row the fill
	// wrote is not refused by the confirm.
	runner := TriggeredByFromContext(ctx)
	if _, err := s.queries.UpdateTranslationTMFuzzy(ctx, db.UpdateTranslationTMFuzzyParams{
		ID:             translationID,
		ActiveText:     &targetText,
		ActiveRichText: ptr.NilIfZero(targetRichText),
		ActorID:        runner,
	}); err != nil {
		// Pgx.ErrNoRows means the row's eligibility flipped between
		// ListTMTargetableByFile and this write — most commonly a linguist
		// confirmed the segment in another tab. Bail out of the auto-approval
		// path so we don't UpdateTranslationHuman a row we couldn't even
		// fill — that would clobber the just-confirmed approval.
		if errors.Is(err, pgx.ErrNoRows) {
			return false
		}
		logging.Error(ctx, err, "failed to store TM batch match", "pipeline-tm-match-store-failed", logging.TranslationID(translationID))
		return false
	}
	if autoConfirm {
		if _, err := s.queries.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
			ID:        translationID,
			HumanText: &targetText,
			// UpdateTranslationHuman assigns active_rich_text unconditionally, so
			// the auto-approve would blank the tags the fill above just wrote.
			// Carry them through.
			HumanRichText: ptr.NilIfZero(targetRichText),
			ActorID:       runner,
		}); errors.Is(err, pgx.ErrNoRows) {
			// Another user opened the segment between the fill and the
			// confirm: the fill stands unconfirmed and keeps its provenance.
			logging.Info(ctx, "TM match left unconfirmed: the segment became held", "pipeline-tm-confirm-held", logging.TranslationID(translationID))
		} else if err != nil {
			logging.Error(ctx, err, "failed to apply TM batch match", "pipeline-tm-match-apply-failed", logging.TranslationID(translationID))
			return false
		}
	}
	matchPercent := percent
	tmIDLocal := tmID
	tmEntryIDLocal := tmEntryID
	s.EmitAutomationHistory(ctx, AutomationEmitOpts{
		TranslationID:  translationID,
		Kind:           db.TranslationHistoryKindTm,
		Origin:         db.TranslationOriginTm,
		Text:           &targetText,
		CreatedBy:      runner,
		TmID:           &tmIDLocal,
		TmEntryID:      &tmEntryIDLocal,
		TmMatchPercent: &matchPercent,
		Details:        map[string]any{"source": "pipeline"},
	})
	return true
}

// Mirrors the sidebar handler's limit so both surfaces pick the same hit
// when the Lev winner ranks outside the top few in pg_trgm DESC order.
const pipelineFuzzyCandidateLimit = 30

// tmMatchConfig is one run's TM matching configuration: the language pair being
// matched plus every knob that decides whether a candidate survives and at what
// score. Bundled rather than passed as five parallel arguments because it
// threads through the whole phase (exact → fuzzy → rescore) unchanged.
type tmMatchConfig struct {
	sourceLang, targetLang string
	fuzzyThreshold         float32
	penalties              translationmemory.Penalties
	// settings.TMLookupOrder*: whether TM priority or match class decides
	// which TM fills a segment. Only the TM phase reads it; the <tm_match>
	// placeholder is fuzzy-only, where both orders agree.
	lookupOrder string
}

// First TM with a passing rescored candidate wins — keeps user-configured
// TM priority over best-score-across-TMs. Does NOT auto-approve.
// `progressBase` keeps the reported total monotonic across exact + fuzzy.
func (s *Service) applyFuzzyTMMatches(
	ctx context.Context,
	tmIDs []int64,
	cfg tmMatchConfig,
	tmInput []db.ListTMTargetableByFileRow,
	matched map[int64]bool,
	progressBase int,
	onProgress ProgressFunc,
) tmFilled {
	var filled tmFilled
	// Each window's lookups run in one session; its matches are written
	// after the session ends, so the phase never holds two connections.
	type fuzzyHit struct {
		row   db.ListTMTargetableByFileRow
		best  db.FuzzyLookupTMRow
		score float64
	}
	for window := range slices.Chunk(tmInput, fuzzySessionSize) {
		var hits []fuzzyHit
		s.withFuzzySession(ctx, func(lookups *fuzzySession) {
			for _, row := range window {
				if matched[row.ID] {
					continue
				}
				if best, score, found := lookups.findBest(tmIDs, cfg, row.SourceText); found {
					hits = append(hits, fuzzyHit{row, best, score})
				}
			}
		})
		for _, h := range hits {
			// Tag re-anchoring, same as the exact path. A fuzzy hit's text
			// differs from the source, so this usually lands on the
			// TransferTags projection rather than a positional remap.
			fuzzyRich := tmTargetRichText(
				ptr.Deref(h.row.SourceRichText), h.row.SourceText,
				h.best.TargetText, ptr.Deref(h.best.TargetRichText))
			if s.applyFuzzyMatch(ctx, h.row.ID, h.best, fuzzyRich, h.score) {
				matched[h.row.ID] = true
				filled.record(h.best.TmID, false)
			}
		}
		if onProgress != nil {
			onProgress("tm", progressBase+filled.total(), len(tmInput))
		}
	}
	return filled
}

// Scores through translationmemory.HitScore, the scoring the editor's TM
// panel uses too.
//
// Penalties are charged before the threshold test, so a penalised candidate has
// to clear a slightly higher bar than a clean one — and loses the tiebreak
// against an equally-similar unpenalised hit.
func (s *Service) bestRescoredCandidate(query string, cfg tmMatchConfig, tmID int64, candidates []db.FuzzyLookupTMRow) (db.FuzzyLookupTMRow, float64, bool) {
	var best db.FuzzyLookupTMRow
	var bestScore float64
	found := false
	for _, hit := range candidates {
		score := translationmemory.HitScore(s.scorer, cfg.penalties, query, tmID, cfg.sourceLang, cfg.targetLang, hit)
		if score < float64(cfg.fuzzyThreshold) {
			continue
		}
		if !found || score > bestScore {
			best = hit
			bestScore = score
			found = true
		}
	}
	return best, bestScore, found
}

// A fuzzy hit is never auto-confirmed — it's a suggestion, not an answer.
func (s *Service) applyFuzzyMatch(ctx context.Context, translationID int64, hit db.FuzzyLookupTMRow, targetRichText string, score float64) bool {
	return s.applyTMFill(ctx, translationID, hit.TmID, hit.ID, hit.TargetText, targetRichText, translationmemory.ScoreToPercent(score), false)
}

// decodeEngineSettings reads an engine's stored options. An empty column is a
// valid "no options", which is what every engine starts with.
func decodeEngineSettings(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
