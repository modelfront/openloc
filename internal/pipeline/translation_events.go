package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	db "openloc/internal/database/sqlcgen"

	"openloc/internal/logging"
)

// translation_events.go — emission helpers that pair every text-changing
// write to `translations` with a row in `translation_history`.
//
// Two flavors:
//
//   1. EmitAutomationHistory — for kinds 'tm' / 'mt' / 'ape' / 'file_import'.
//      Runs the (UpdateTranslationActiveSource + InsertTranslationHistory)
//      pair inside one tx so the denormalized lineage on translations
//      (origin + tm_match_percent) cannot drift from the latest history row.
//      Caller is expected to have ALREADY written the new text to the
//      translation (via UpdateTranslationTMFuzzy / UpdateTranslationMT /
//      etc.) — this helper only handles the audit + lineage pair.
//
//   2. EmitHumanHistory — for kinds 'human' / 'propagation' / 'cleared' and
//      the status events 'approved' / 'auto_approved' / 'unconfirmed'.
//      Inserts the history row but does NOT touch translations.origin or
//      translations.tm_match_percent: these columns are sticky through human
//      edits — they describe the latest automation lineage and survive
//      human action.
//      The status events additionally carry no Text, so they never become the
//      edit-distance baseline (GetLatestHistoryTextForTranslation).
//
// Both helpers are BEST-EFFORT for the history insert: failure logs but
// does not propagate, matching the migration 022 / TM-history precedent.
// The reasoning: the canonical write (text update) already succeeded; an
// audit-row failure is a secondary concern. If audit reliability ever
// becomes a hard requirement, wrap the caller in an outer tx that includes
// the parent write — the helpers are designed to compose into that flow.
//
// Both helpers are package-level functions so any layer (pipeline workers,
// HTTP handlers, async jobs) can call them without depending on a Service
// receiver. The pipeline.Service has thin method wrappers that pre-bind
// `s.txr` / `s.queries` so existing in-package callers stay tidy.

// AutomationEmitOpts carries the fields needed to record a tm/mt/ape/file_import
// event. All pointer / nullable fields are sparse — pass nil when the kind
// doesn't carry that piece of provenance.
type AutomationEmitOpts struct {
	TranslationID int64
	Kind          db.TranslationHistoryKind // must be 'tm' | 'mt' | 'ape' | 'file_import'
	Origin        db.TranslationOrigin      // matching enum value for translations.origin
	Text          *string                   // target text snapshot
	RichText      *string
	CreatedBy     *int64 // user who triggered the upstream pipeline (uploader / Pre-translate clicker / panel-insert clicker)
	TmID          *int64 // kind='tm' only
	TmEntryID     *int64 // kind='tm' only
	MtEngineID    *int64 // kind='mt' / 'ape', vendor engines only
	// AIEngineID attributes the row to an AI engine instead. Separate from
	// MtEngineID on purpose: "DeepL" and "GPT-4o behind prompt X" are not
	// comparable units, so they get separate measures rather than one mixed
	// engine dimension.
	AIEngineID          *int64         // kind='mt' / 'ape', AI engines only
	TmMatchPercent      *int16         // kind='tm' only
	JobStepAssignmentID *int64         // the active workflow step when this happened
	Details             map[string]any // free-form payload; marshaled to jsonb; nil → '{}'
}

// EmitAutomationHistory writes the history row + updates the denormalized
// lineage on translations in one tx via the given TxRunner. Best-effort:
// logs + swallows tx failures.
//
// If the caller is already inside a transaction (e.g. file upload), use
// EmitAutomationHistoryInTx with the caller's transactional Querier
// instead — nesting transactions via TxRunner.InTx works via savepoints
// but is unnecessary indirection when the parent already owns a tx.
func EmitAutomationHistory(ctx context.Context, txr TxRunner, opts AutomationEmitOpts) {
	if txr == nil {
		logging.Error(ctx, nil, "automation history not recorded: no transaction runner", "history-automation-no-txr", historyAttrs(opts.TranslationID, opts.Kind)...)
		return
	}
	if err := txr.InTx(ctx, func(q db.Querier) error {
		return emitAutomationHistoryInQuerier(ctx, q, opts)
	}); err != nil {
		logging.Error(ctx, err, "automation history emit failed", "history-automation-emit-failed", historyAttrs(opts.TranslationID, opts.Kind)...)
	}
}

// EmitAutomationHistoryInTx runs the same (UpdateTranslationActiveSource +
// InsertTranslationHistory) pair directly on a tx-bound Querier. Use this
// when the caller is already inside a tx and wants the audit pair to commit
// atomically with the parent write (file upload's XLSX bilingual import is
// the canonical example).
//
// Returns the error so the caller can decide whether to roll back the
// parent tx on emission failure. The TxRunner-based variant always
// swallows.
func EmitAutomationHistoryInTx(ctx context.Context, q db.Querier, opts AutomationEmitOpts) error {
	if q == nil {
		return fmt.Errorf("nil querier")
	}
	return emitAutomationHistoryInQuerier(ctx, q, opts)
}

func emitAutomationHistoryInQuerier(ctx context.Context, q db.Querier, opts AutomationEmitOpts) error {
	detailsBytes, err := marshalDetails(opts.Details)
	if err != nil {
		return fmt.Errorf("marshal details: %w", err)
	}
	if err := q.UpdateTranslationActiveSource(ctx, db.UpdateTranslationActiveSourceParams{
		ID:             opts.TranslationID,
		Origin:         db.NullTranslationOrigin{TranslationOrigin: opts.Origin, Valid: true},
		TmMatchPercent: opts.TmMatchPercent,
	}); err != nil {
		return fmt.Errorf("update active_source: %w", err)
	}
	if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID:       opts.TranslationID,
		Kind:                opts.Kind,
		Text:                opts.Text,
		RichText:            opts.RichText,
		CreatedBy:           opts.CreatedBy,
		TmID:                opts.TmID,
		TmEntryID:           opts.TmEntryID,
		MtEngineID:          opts.MtEngineID,
		AiEngineID:          opts.AIEngineID,
		TmMatchPercent:      opts.TmMatchPercent,
		JobStepAssignmentID: opts.JobStepAssignmentID,
		Details:             detailsBytes,
	}); err != nil {
		return fmt.Errorf("insert history: %w", err)
	}
	return nil
}

// EmitAutomationHistory is the Service-bound convenience wrapper that
// pre-binds `s.txr`. Existing in-pipeline callers (translate.go, predict.go)
// reach for this so they don't have to pass the TxRunner explicitly.
func (s *Service) EmitAutomationHistory(ctx context.Context, opts AutomationEmitOpts) {
	if s == nil {
		logging.Error(ctx, nil, "automation history not recorded: nil service", "history-automation-nil-service", historyAttrs(opts.TranslationID, opts.Kind)...)
		return
	}
	EmitAutomationHistory(ctx, s.txr, opts)
}

// HumanEmitOpts captures a human or propagation history row. These kinds
// MUST NOT update translations.origin / tm_match_percent.
type HumanEmitOpts struct {
	TranslationID int64
	// 'human' | 'propagation' | 'cleared', or a status event carrying no Text:
	// 'approved' | 'auto_approved' | 'unconfirmed'. Anything that leaves the
	// denormalized lineage on `translations` untouched belongs here;
	// AutomationEmitOpts is for the four kinds that update it.
	Kind                db.TranslationHistoryKind
	Text                *string
	RichText            *string
	CreatedBy           *int64
	EditDistance        *int32
	EditingTimeMs       *int64 // per-event editing time delta; nil for non-human
	JobStepAssignmentID *int64
	// AIEngineID names the AI engine behind an 'auto_approved' event, so the
	// approval charts by engine like the APE rows do.
	AIEngineID *int64
	Details    map[string]any
}

// EmitHumanHistory inserts the audit row only. No tx wrapper needed (single
// statement). Best-effort: logs + swallows failures.
func EmitHumanHistory(ctx context.Context, q db.Querier, opts HumanEmitOpts) {
	if q == nil {
		logging.Error(ctx, nil, "human history not recorded: nil querier", "history-human-nil-querier", historyAttrs(opts.TranslationID, opts.Kind)...)
		return
	}
	detailsBytes, err := marshalDetails(opts.Details)
	if err != nil {
		logging.Error(ctx, err, "history details could not be encoded", "history-human-marshal-failed", historyAttrs(opts.TranslationID, opts.Kind)...)
		return
	}
	if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID:       opts.TranslationID,
		Kind:                opts.Kind,
		Text:                opts.Text,
		RichText:            opts.RichText,
		CreatedBy:           opts.CreatedBy,
		EditDistance:        opts.EditDistance,
		EditingTimeMs:       opts.EditingTimeMs,
		JobStepAssignmentID: opts.JobStepAssignmentID,
		AiEngineID:          opts.AIEngineID,
		Details:             detailsBytes,
	}); err != nil {
		logging.Error(ctx, err, "human history emit failed", "history-human-emit-failed", historyAttrs(opts.TranslationID, opts.Kind)...)
	}
}

// EmitHumanHistory is the Service-bound convenience wrapper.
func (s *Service) EmitHumanHistory(ctx context.Context, opts HumanEmitOpts) {
	if s == nil {
		logging.Error(ctx, nil, "human history not recorded: nil service", "history-human-nil-service", historyAttrs(opts.TranslationID, opts.Kind)...)
		return
	}
	EmitHumanHistory(ctx, s.queries, opts)
}

// historyAttrs are the log attributes every history-emission failure carries.
func historyAttrs(translationID int64, kind db.TranslationHistoryKind) []slog.Attr {
	return []slog.Attr{logging.TranslationID(translationID), logging.Label("history_kind", string(kind))}
}

func marshalDetails(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil // sqlc COALESCEs to '{}'
	}
	return json.Marshal(m)
}
