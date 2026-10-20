package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Change-reason values written into translation_memory_history by the
// pipeline write-back paths. Mirrors the existing 'edit' / 'find_replace'
// values produced by the TM browser inline-edit and bulk Find&Replace
// handlers (see translationmemory/handler.go).
const (
	TMReasonCATConfirm = "cat_confirm"
	// Stored in history rows and keyed on by the TM history dialog.
	TMReasonCompletionBulk = "feedback_bulk"
)

// SaveSegmentToTM writes a single source/target pair to all write-mode TMs
// for the given project and language pair. This is the core TM-write logic
// shared by both auto-save (on segment confirm) and the bulk write on completion.
//
// `sourceRichText` / `targetRichText` carry the inline-tag-bearing form of
// the text when present. Pass "" if the segment/translation has no inline
// tags — the SQL maps "" → NULL via NULLIF, and the display layer falls
// back to plain text.
//
// `changedByUserID` is the user whose action caused this write (nil for
// system contexts such as asynq workers). `changeReason` discriminates the
// caller: TMReasonCATConfirm for CAT editor confirms,
// TMReasonCompletionBulk for the bulk write on completion. Both feed
// translation_memory_history when the upsert overwrites an existing row.
//
// Dedupe is on source only. If a row already exists for this
// source on the human path, this call overwrites its target_text and
// snapshots the previous state into translation_memory_history (in the
// same transaction) — unless the new target equals the old target, in
// which case history is skipped to avoid no-op spam.
//
// Returns the IDs of TMs that successfully received the write — not the
// list of TMs that were CONFIGURED to receive it. A TM whose upsert errored
// is logged + skipped + omitted from the returned slice. Callers (FE cache
// invalidation in particular) need ground truth: what was
// actually written, not what was supposed to be written. Returning the
// configured list would invalidate caches that didn't receive the new entry,
// triggering needless refetches and confusing the audit log.
//
// Tenant scoping: caller passes tenantID. The project lookup is tenant-scoped
// via GetProject — same defense-in-depth as the rest of the pipeline service
// surface (TranslateFile, RunAIStep, etc.). Without it, a stale projectID
// from a deleted/cross-tenant caller would fall through to the write path
// silently.
func (s *Service) SaveSegmentToTM(ctx context.Context, tenantID int64, sourceText, targetText, sourceLang, targetLang, sourceRichText, targetRichText string, projectID int64, changedByUserID *int64, changeReason string) ([]int64, error) {
	// No document context recorded → the entry can never be a 101% in-context
	// exact match; it degrades to plain 100%. Callers that know the
	// segment's neighbors (CAT confirm, completion TM write-back) use
	// saveSegmentToTM.
	return s.saveSegmentToTM(ctx, tenantID, sourceText, targetText, sourceLang, targetLang, sourceRichText, targetRichText, projectID, changedByUserID, changeReason, "", "")
}

// saveSegmentToTM is the core write path, carrying the neighbor-context
// hashes (md5 of the segment's previous/next source text; "" = no context →
// stored as NULL). Last-write-wins via UpsertTMPipelineEntry. A TM whose
// upsert fails is logged and left out of the returned IDs.
func (s *Service) saveSegmentToTM(ctx context.Context, tenantID int64, sourceText, targetText, sourceLang, targetLang, sourceRichText, targetRichText string, projectID int64, changedByUserID *int64, changeReason, prevSourceHash, nextSourceHash string) ([]int64, error) {
	saved, failed, err := s.writeSegmentToTMs(ctx, tenantID, sourceText, targetText, sourceLang, targetLang, sourceRichText, targetRichText, projectID, changedByUserID, changeReason, prevSourceHash, nextSourceHash)
	for _, f := range failed {
		logging.Error(ctx, f.err, "failed to upsert TM entry", "tm-write-upsert-failed", logging.TMID(f.tmID))
	}
	return saved, err
}

// tmUpsertFailure is one write TM that refused a segment.
type tmUpsertFailure struct {
	tmID int64
	err  error
}

// writeSegmentToTMs is saveSegmentToTM returning the refused upserts instead
// of logging them, for a caller that tells one kind of failure from another.
func (s *Service) writeSegmentToTMs(ctx context.Context, tenantID int64, sourceText, targetText, sourceLang, targetLang, sourceRichText, targetRichText string, projectID int64, changedByUserID *int64, changeReason, prevSourceHash, nextSourceHash string) ([]int64, []tmUpsertFailure, error) {
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		return nil, nil, fmt.Errorf("get project for TM write: %w", err)
	}
	writeTMs, err := s.queries.LookupSettingsWriteTMs(ctx, db.LookupSettingsWriteTMsParams{
		SettingsProfileID: project.SettingsProfileID,
		SourceLanguage:    sourceLang,
		TargetLanguage:    targetLang,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("lookup write TMs: %w", err)
	}
	if len(writeTMs) == 0 {
		logging.Warn(ctx, nil, "no write TM for the language pair; segment not saved to TM", "tm-write-segment-no-write-tm",
			append(logging.Langs(sourceLang, targetLang), logging.ProjectID(projectID))...)
		return nil, nil, nil
	}

	// Stamp the triggering user's "Name <email>" label into updated_by
	// (falls back to "human" for system/asynq writes with no user in scope).
	updatedBy := s.updatedByLabel(ctx, changedByUserID)

	saved := make([]int64, 0, len(writeTMs))
	var failed []tmUpsertFailure
	for _, tmID := range writeTMs {
		if err := s.upsertTMWithHistory(ctx, tmID, sourceLang, targetLang, sourceText, targetText, sourceRichText, targetRichText, updatedBy, changedByUserID, changeReason, prevSourceHash, nextSourceHash); err != nil {
			failed = append(failed, tmUpsertFailure{tmID: tmID, err: err})
			continue
		}
		saved = append(saved, tmID)
	}
	return saved, failed, nil
}

// unstorableInTM reports whether err is the database refusing this one
// segment for good (a data or size-limit error, SQLSTATE class 22 or 54, such
// as a source text too long for the exact-match index), so retrying cannot
// help. Anything else may pass.
func unstorableInTM(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "54")
}

// upsertTMWithHistory wraps UpsertTMPipelineEntry + conditional InsertTMHistory
// in a single DB transaction. The atomicity guarantee is what justifies the
// TxRunner dependency on Service: if the history insert fails after the
// upsert succeeded, both roll back and the caller can retry.
//
// Concurrency: at the start of the tx we acquire a per-source advisory lock
// (pg_advisory_xact_lock keyed on tm_id + source_text + lang pair). Two
// parallel SaveSegmentToTM calls for the same source serialize on this lock
// so the loser's `prev` SELECT inside UpsertTMPipelineEntry's CTE sees the
// winner's committed row. Without this lock, both parallel calls would
// snapshot empty `prev`, the loser's LEFT JOIN would resolve prev_entry_id
// to NULL, and the winner's target would be silently lost from BOTH the
// live row AND the audit log — a real data-loss bug.
//
// TxRunner abstraction: production passes PgxTxRunner (real pool, real tx
// + advisory lock acquired against Postgres). Mock-querier unit tests pass
// a passthrough TxRunner that runs the callback against the in-memory mock
// without a real tx — the AcquireTMSourceLock + UpsertTMPipelineEntry
// + InsertTMHistory calls hit the mock, which returns whatever the test
// configured. The history contract is covered by the integration tests in
// integration_test.go (TestIntegrationTMWriteBack_*,
// TestIntegrationFeedbackHistoryReason,
// TestIntegrationTMWriteBack_ConcurrentDifferentTargetsArchiveLoser) using
// a real Postgres via testutil.SetupTestDB.
func (s *Service) upsertTMWithHistory(ctx context.Context, tmID int64, sourceLang, targetLang, sourceText, targetText, sourceRichText, targetRichText, updatedBy string, changedByUserID *int64, changeReason, prevSourceHash, nextSourceHash string) error {
	return s.txr.InTx(ctx, func(q db.Querier) error {
		return upsertTMEntryInTx(ctx, q, tmID, sourceLang, targetLang, sourceText, targetText, sourceRichText, targetRichText, updatedBy, changedByUserID, changeReason, prevSourceHash, nextSourceHash)
	})
}

// upsertTMEntryInTx is upsertTMWithHistory's body, run on a querier that is
// bound to a transaction (the advisory lock lasts until it ends).
func upsertTMEntryInTx(ctx context.Context, q db.Querier, tmID int64, sourceLang, targetLang, sourceText, targetText, sourceRichText, targetRichText, updatedBy string, changedByUserID *int64, changeReason, prevSourceHash, nextSourceHash string) error {
	// Serialize concurrent saves for the same source so prev resolution
	// in the CTE below sees a consistent committed view.
	if err := q.AcquireTMSourceLock(ctx, db.AcquireTMSourceLockParams{
		TmID:           tmID,
		SourceLanguage: sourceLang,
		TargetLanguage: targetLang,
		SourceText:     sourceText,
	}); err != nil {
		return fmt.Errorf("acquire TM source lock: %w", err)
	}

	out, err := q.UpsertTMPipelineEntry(ctx, db.UpsertTMPipelineEntryParams{
		TmID:           tmID,
		SourceLanguage: sourceLang,
		TargetLanguage: targetLang,
		SourceText:     sourceText,
		TargetText:     targetText,
		UpdatedBy:      updatedBy,
		SourceRichText: sourceRichText,
		TargetRichText: targetRichText,
		PrevSourceHash: prevSourceHash,
		NextSourceHash: nextSourceHash,
	})
	if err != nil {
		return fmt.Errorf("upsert TM entry: %w", err)
	}

	// PrevEntryID is non-nil only when DO UPDATE fired (an existing
	// human-path row was overwritten). When nil this was a fresh INSERT
	// — nothing to archive.
	if out.PrevEntryID != nil {
		var prevTarget string
		if out.PrevTargetText != nil {
			prevTarget = *out.PrevTargetText
		}
		// Skip history when the target didn't actually change.
		// Translators re-confirming an unchanged segment shouldn't
		// pollute the audit trail. usage_count still bumps via the upsert.
		if prevTarget != targetText {
			if err := q.InsertTMHistory(ctx, db.InsertTMHistoryParams{
				EntryID:         *out.PrevEntryID,
				SourceText:      sourceText, // hash-equal to prev source on the dedupe key
				TargetText:      prevTarget,
				SourceRichText:  out.PrevSourceRichText,
				TargetRichText:  out.PrevTargetRichText,
				ChangedByUserID: changedByUserID,
				ChangeReason:    changeReason,
			}); err != nil {
				return fmt.Errorf("insert TM history: %w", err)
			}
		}
	}
	return nil
}

// SaveToTM resolves the full chain from a translation ID and writes to TM.
// Used for auto-save when a human confirms a segment in the CAT editor.
// Best-effort: logs errors but does not fail the caller.
//
// Returns the list of TM IDs that successfully received the write so the
// HTTP handler can echo it back to the FE for cache invalidation. Empty
// slice + nil error = nothing was written (row not approved, no configured
// write TMs, etc.); the FE treats this as
// "no TM caches to invalidate".
//
// Tenant scoping: caller passes tenantID. The
// translation lookup uses GetTranslationTenant as the entry guard — same
// pattern as qa/service.go::CheckTranslationProposal. Subsequent unscoped
// Get* calls are FK-safe (translations.segment_id → segments.file_id →
// files.project_id → projects.tenant_id is enforced by the schema). Each
// downstream call carries an inline `safe:` rationale.
func (s *Service) SaveToTM(ctx context.Context, tenantID, translationID int64) ([]int64, error) {
	// Step 1: tenant-scoped entry guard. Cross-tenant translationID returns
	// ErrNoRows — the caller (translation handler) maps to 404.
	tr, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
		ID:       translationID,
		TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("get translation: %w", err)
	}
	// Only human-approved rows go to the TM: `auto_approved` (QP+APE) output
	// would degrade the model. A human editing an auto_approved row
	// flips it to `approved` via UpdateTranslationHuman, making it eligible.
	// The text written is `active_text` (for an approved row this is the
	// human's confirmed target).
	if tr.Status != db.TranslationStatusApproved {
		return nil, nil
	}
	// Same invariant for an assistant confirm: no human decided this text, so
	// it must not become leverage. A later human confirm resets confirmed_via.
	if tr.ConfirmedVia == "assistant" {
		return nil, nil
	}
	if tr.ActiveText == nil {
		logging.Warn(ctx, nil, "approved translation has no active text; TM write skipped", "tm-write-no-active-text", logging.TranslationID(translationID))
		return nil, nil
	}

	// safe: tr.SegmentID is FK-bound to a translations row owned by tenantID.
	seg, err := s.queries.GetSegment(ctx, tr.SegmentID)
	if err != nil {
		return nil, fmt.Errorf("get segment: %w", err)
	}

	// safe: seg.FileID is FK-bound through the validated tr → seg chain.
	f, err := s.queries.GetFile(ctx, seg.FileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}

	// safe: f.ProjectID is FK-bound through the validated chain above.
	project, err := s.queries.GetProjectByID(ctx, f.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}

	srcRich := ""
	if seg.SourceRichText != nil {
		srcRich = *seg.SourceRichText
	}
	tgtRich := ""
	if tr.ActiveRichText != nil {
		tgtRich = *tr.ActiveRichText
	}

	// Record the segment's document neighbors so a future
	// pretranslate of the same context can leverage this entry as 101%.
	prevHash, nextHash := s.resolveContextHashes(ctx, tr.SegmentID)
	tmIDs, err := s.saveSegmentToTM(ctx, tenantID, seg.SourceText, *tr.ActiveText, project.SourceLanguage, tr.TargetLanguage, srcRich, tgtRich, project.ID, userIDFromCtx(ctx), TMReasonCATConfirm, prevHash, nextHash)
	if err != nil {
		return nil, err
	}

	// The write-back leaves the segment's origin / tm_match_percent alone:
	// writing to the TM is not leveraging from it. Only
	// a later Apply-TM / pre-translate that matches this source stamps
	// origin='tm'. The TM write is audited in translation_memory_history.

	return tmIDs, nil
}

// userIDFromCtx returns a pointer to the user ID from the auth context, or
// nil for system-driven calls (asynq workers, background jobs, unit tests)
// where no auth claims have been attached. translation_memory_history.
// changed_by_user_id is nullable to accommodate both cases.
//
// Uses ClaimsFromContext (not UserIDFromContext) because this is called
// from pipeline code paths that may run without auth claims — the latter
// panics on missing claims.
func userIDFromCtx(ctx context.Context) *int64 {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims == nil || claims.UserID == 0 {
		return nil
	}
	uid := claims.UserID
	return &uid
}

// updatedByLabel resolves the free-text updated_by label for a TM write from
// the acting user's id. Returns "human" for system-driven writes
// (nil userID, asynq workers, background jobs) or when the user can't be
// loaded — matching the historical literal so those rows read sensibly.
// Otherwise returns auth.UserLabel("Name <email>").
func (s *Service) updatedByLabel(ctx context.Context, userID *int64) string {
	if userID == nil {
		return "human"
	}
	u, err := s.queries.GetUser(ctx, *userID)
	if err != nil {
		logging.Warn(ctx, err, "could not resolve user for updated_by label; using 'human'", "tm-write-user-label-failed", logging.UserID(*userID))
		return "human"
	}
	return auth.UserLabel(u.Name, u.Email)
}
