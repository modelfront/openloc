package file

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
	"openloc/internal/pipeline"
	"openloc/internal/projectevents"
	"openloc/internal/ptr"
)

// ErrDuplicateXLIFFKeys signals that the uploaded XLIFF contains two or more
// `<trans-unit>` elements with the same composite key (id[:mid]). The handler
// should map this to a 400 Bad Request and surface the offending keys so the
// user can fix the upload.
//
// Why we reject instead of silently de-duplicating: a duplicate-key XLIFF lets
// only the LAST occurrence land. The first occurrence's edits are lost
// without any signal to the user — the worst possible failure mode for a
// round-trip flow. Duplicate keys are a 400, never a silent last-wins.
type DuplicateKeysError struct {
	Keys []string
}

func (e *DuplicateKeysError) Error() string {
	return fmt.Sprintf("duplicate trans-unit keys in upload: %v", e.Keys)
}

// ReimportXLIFFRequest is the validated input passed from the HTTP handler to
// the re-import primitive. Inputs are pre-validated upstream (file ownership,
// language belongs to project, content parsed) so this layer only owns the
// diff/apply logic.
//
// EditedBy records the admin who triggered the re-import. Re-imported text
// comes from outside OpenLoc, but the operation itself is admin-attributable
// — translations.edited_by reflects the user who pressed "apply." Required
// (non-zero) for real runs. Dry runs use it too: the segments another user
// has open are skipped, and the importer's own are not.
type ReimportXLIFFRequest struct {
	FileID           int64
	TargetLanguage   string
	IncomingSegments []parser.Segment
	Force            bool
	DryRun           bool
	EditedBy         int64
	// Audit context for the project_events row. REQUIRED
	// for non-dry-run calls: EmitInTx runs inside the apply tx, so a zero
	// ProjectID/TenantID fails the FK and aborts the whole reimport. The
	// handler already resolved the file tenant-scoped; passing these through
	// avoids a re-fetch. ActorEmail is the render-safe snapshot kept in
	// details after the user row is deleted (actor_id FK is SET NULL).
	ProjectID  int64
	TenantID   int64
	FileName   string
	ActorEmail string
	// BeforeWrite, when set, runs in the re-import transaction right before
	// the first translation is written, and only when one will be: a dry run,
	// a run that needs force, or one that changes nothing never calls it. The
	// task edit guard reopens a completed task here, so the reopen lands or
	// rolls back with the text.
	BeforeWrite func(ctx context.Context, tx pgx.Tx) error
}

// MaxKeysPerCategory caps the number of keys returned in each per-category
// list. Counts are NOT capped — the user always sees the true total. Lists
// are capped so a 50k-segment XLIFF doesn't produce a multi-megabyte JSON
// response (and so the re-import modal can render the lists without
// virtualizing them). When a list is capped, the corresponding `*Truncated`
// flag is set so the UI can hint "showing 100 of 1247."
const MaxKeysPerCategory = 100

// ReimportXLIFFResult is the conflict report returned to the API caller.
// Counts cover every translation row in the file × target_language; the
// per-category key lists exist so the frontend "preview" modal can show the
// user *which* segments would be skipped before they decide to force.
type ReimportXLIFFResult struct {
	Updated           int  `json:"updated"`
	SkippedLocked     int  `json:"skipped_locked"`
	SkippedHeld       int  `json:"skipped_held"`
	SkippedApproved   int  `json:"skipped_approved"`
	UntouchedNoChange int  `json:"untouched_no_change"`
	ExtraInUpload     int  `json:"extra_in_upload"`
	MissingFromUpload int  `json:"missing_from_upload"`
	ForceRequired     bool `json:"force_required"`
	Applied           bool `json:"applied"`

	SkippedLockedKeys     []string `json:"skipped_locked_keys,omitempty"`
	SkippedHeldKeys       []string `json:"skipped_held_keys,omitempty"`
	SkippedApprovedKeys   []string `json:"skipped_approved_keys,omitempty"`
	ExtraInUploadKeys     []string `json:"extra_in_upload_keys,omitempty"`
	MissingFromUploadKeys []string `json:"missing_from_upload_keys,omitempty"`

	// *Truncated flags signal that the corresponding *Keys list was capped at
	// MaxKeysPerCategory. The COUNT field is always the true total.
	SkippedLockedTruncated     bool `json:"skipped_locked_truncated,omitempty"`
	SkippedHeldTruncated       bool `json:"skipped_held_truncated,omitempty"`
	SkippedApprovedTruncated   bool `json:"skipped_approved_truncated,omitempty"`
	ExtraInUploadTruncated     bool `json:"extra_in_upload_truncated,omitempty"`
	MissingFromUploadTruncated bool `json:"missing_from_upload_truncated,omitempty"`
	// TaskReopened: the apply reopened the job's completed task. Sent beside
	// the report (task_reopened), not inside it.
	TaskReopened bool `json:"-"`
}

// appendCapped pushes `key` onto `*list` only when the list has fewer than
// MaxKeysPerCategory entries. Sets `*truncated` to true once the cap is hit
// and stays true. Counts are tracked separately by the caller.
func appendCapped(list *[]string, truncated *bool, key string) {
	if len(*list) >= MaxKeysPerCategory {
		*truncated = true
		return
	}
	*list = append(*list, key)
}

// reimportPlan is the internal queue of writes to apply once the diff has been
// classified. Building this list during diff means the apply loop is dumb and
// the diff is purely a function of inputs — easy to unit-test.
type reimportPlan struct {
	translationID int64
	key           string
	newText       string
	newRichText   *string
}

// ReimportXLIFF runs the diff against current DB state and (when
// `dry_run=false` and either no approved segments require force or `force=true`)
// applies all updates within a single transaction. Atomicity is the load-bearing
// invariant: either every queued update lands or none do.
//
// The transaction is opened even for dry-run so the diff is computed against
// a consistent snapshot (avoids "preview said 12, apply said 11" surprises if
// another writer races between calls). Dry-run always rolls back.
//
// The caller is responsible for:
//   - tenant-scoping the file lookup (HandleReimportXLIFF uses GetFileTenant)
//   - validating that TargetLanguage is in the project's target list
//   - parsing the uploaded content into IncomingSegments
//
// Returns the conflict report with `Applied` indicating whether writes landed.
// `Applied=false` happens for dry-run runs and for force-required runs without
// `force=true` — in both cases the report is still useful as a preview.
func ReimportXLIFF(ctx context.Context, pool *pgxpool.Pool, req ReimportXLIFFRequest) (*ReimportXLIFFResult, error) {
	if dups := findDuplicateKeys(req.IncomingSegments); len(dups) > 0 {
		return nil, &DuplicateKeysError{Keys: dups}
	}

	// A re-import that may write locks the job before it reads the diff, so
	// it queues behind a step change instead of deadlocking with its recount.
	var jobIDs []int64
	if !req.DryRun {
		var err error
		if jobIDs, err = database.FileJobIDs(ctx, db.New(pool), req.FileID, req.TargetLanguage); err != nil {
			return nil, fmt.Errorf("resolve job for re-import: %w", err)
		}
	}
	var result *ReimportXLIFFResult
	err := database.InJobsTx(ctx, pool, req.TenantID, jobIDs, func(tx pgx.Tx, txq db.Querier) error {
		editedByPtr := ptr.NilIfZero(req.EditedBy)
		rows, err := txq.ListTranslationsByFileForReimport(ctx, db.ListTranslationsByFileForReimportParams{
			FileID:         req.FileID,
			TargetLanguage: req.TargetLanguage,
			ActorID:        editedByPtr,
		})
		if err != nil {
			return fmt.Errorf("list translations for re-import: %w", err)
		}

		var plan []reimportPlan
		result, plan = computeReimportDiff(rows, req.IncomingSegments, req.Force)

		shouldApply := !req.DryRun && (!result.ForceRequired || req.Force)
		if !shouldApply {
			// Dry-run OR force is required but caller didn't pass force=true. Either
			// way, no writes land.
			result.Applied = false
			return nil
		}

		if req.BeforeWrite != nil && len(plan) > 0 {
			if err := req.BeforeWrite(ctx, tx); err != nil {
				return err
			}
		}

		written := make([]int64, 0, len(plan))
		for _, p := range plan {
			humanText := p.newText
			n, err := txq.UpdateTranslationFromReimport(ctx, db.UpdateTranslationFromReimportParams{
				ID:            p.translationID,
				HumanText:     &humanText,
				HumanRichText: p.newRichText,
				EditedBy:      editedByPtr,
			})
			if err != nil {
				return fmt.Errorf("apply re-import to translation %d: %w", p.translationID, err)
			}
			if n == 0 {
				// Another user opened the segment after the diff read it.
				result.Updated--
				result.SkippedHeld++
				appendCapped(&result.SkippedHeldKeys, &result.SkippedHeldTruncated, p.key)
				continue
			}
			written = append(written, p.translationID)
			// Provenance lives in translation_history. Emit a
			// `file_import` automation event (origin='file_import') inside the
			// same reimport tx so the audit + lineage commit atomically.
			importedText := p.newText
			if err := pipeline.EmitAutomationHistoryInTx(ctx, txq, pipeline.AutomationEmitOpts{
				TranslationID: p.translationID,
				Kind:          db.TranslationHistoryKindFileImport,
				Origin:        db.TranslationOriginFileImport,
				Text:          &importedText,
				RichText:      p.newRichText,
				CreatedBy:     editedByPtr,
				Details:       map[string]any{"source": "xliff_reimport"},
			}); err != nil {
				return fmt.Errorf("emit reimport history for translation %d: %w", p.translationID, err)
			}
		}

		// The re-import replaced these targets, so their stored QA findings — and
		// any ignore on them — describe text that is gone.
		// Unlike the initial bilingual upload, these translations already existed
		// and can already carry findings. `written` holds only the rows the import
		// actually wrote, so a locked row, or one another user held, keeps its text
		// and findings.
		if len(written) > 0 {
			if err := txq.DeleteQAResultsForTranslations(ctx, written); err != nil {
				return fmt.Errorf("drop stale QA findings for re-import: %w", err)
			}
		}

		// Project-level audit in the same tx, so it rolls back with the writes.
		fileID := req.FileID
		if err := projectevents.EmitInTx(ctx, txq, projectevents.Event{
			ProjectID: req.ProjectID,
			TenantID:  req.TenantID,
			Type:      projectevents.TypeFileReimported,
			ActorID:   editedByPtr,
			ActorName: req.ActorEmail,
			FileID:    &fileID,
			Details: map[string]any{
				"file_name":        req.FileName,
				"target_language":  req.TargetLanguage,
				"updated":          result.Updated,
				"skipped_locked":   result.SkippedLocked,
				"skipped_held":     result.SkippedHeld,
				"skipped_approved": result.SkippedApproved,
				"force":            req.Force,
			},
		}); err != nil {
			return fmt.Errorf("record reimport event: %w", err)
		}
		result.Applied = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// computeReimportDiff classifies each existing (file, target_language)
// translation row against the incoming XLIFF and produces both the user-facing
// counts and the queue of writes to apply.
//
// Rows match incoming segments by key: row.segment_key = incoming.Key, the
// trans-unit id the bilingual export wrote.
//
// Classification per matched row:
//   - incoming target empty + rich empty  → untouched_no_change (R4: no wipe)
//   - incoming target == active           → untouched_no_change
//   - row.locked = true                   → skipped_locked
//   - another user has the row open       → skipped_held
//   - row.status = 'approved' && !force   → skipped_approved (sets force_required)
//   - otherwise                           → update queued
//
// Unmatched rows → missing_from_upload. Unmatched incoming → extra_in_upload.
//
// Pure function — no DB calls. Safe to unit-test without a database.
func computeReimportDiff(
	rows []db.ListTranslationsByFileForReimportRow,
	incoming []parser.Segment,
	force bool,
) (*ReimportXLIFFResult, []reimportPlan) {
	result := &ReimportXLIFFResult{}
	var plan []reimportPlan

	incomingIdxByKey := make(map[string]int, len(incoming))
	for i, s := range incoming {
		if s.Key != "" {
			incomingIdxByKey[s.Key] = i
		}
	}
	// The incoming segments some row claimed; the rest are extras.
	matchedIncoming := make(map[int]bool, len(incoming))

	for _, row := range rows {
		rowKey := ptr.Deref(row.SegmentKey)
		idx, ok := incomingIdxByKey[rowKey]
		if !ok {
			result.MissingFromUpload++
			if rowKey != "" {
				appendCapped(&result.MissingFromUploadKeys, &result.MissingFromUploadTruncated, rowKey)
			}
			continue
		}
		matchedIncoming[idx] = true
		seg := incoming[idx]

		if seg.Target == "" && seg.TargetRichText == "" {
			result.UntouchedNoChange++
			continue
		}

		currentPlain := ptr.Deref(row.ActiveText)
		currentRich := ptr.Deref(row.ActiveRichText)
		if seg.TargetRichText != "" {
			if seg.Target == currentPlain && parser.XLIFFRichTextEqual(seg.TargetRichText, currentRich) {
				result.UntouchedNoChange++
				continue
			}
		} else {
			if seg.Target == currentPlain {
				result.UntouchedNoChange++
				continue
			}
		}

		if row.Locked {
			result.SkippedLocked++
			appendCapped(&result.SkippedLockedKeys, &result.SkippedLockedTruncated, rowKey)
			continue
		}

		if row.HeldByOther {
			result.SkippedHeld++
			appendCapped(&result.SkippedHeldKeys, &result.SkippedHeldTruncated, rowKey)
			continue
		}

		if row.Status == db.TranslationStatusApproved && !force {
			result.SkippedApproved++
			appendCapped(&result.SkippedApprovedKeys, &result.SkippedApprovedTruncated, rowKey)
			result.ForceRequired = true
			continue
		}

		richPtr := ptr.NilIfZero(seg.TargetRichText)
		plan = append(plan, reimportPlan{
			translationID: row.TranslationID,
			key:           rowKey,
			newText:       seg.Target,
			newRichText:   richPtr,
		})
		result.Updated++
	}

	// Extras: incoming segments no row claimed.
	for i, seg := range incoming {
		if matchedIncoming[i] {
			continue
		}
		result.ExtraInUpload++
		appendCapped(&result.ExtraInUploadKeys, &result.ExtraInUploadTruncated, seg.Key)
	}

	return result, plan
}

// findDuplicateKeys returns every key that appears two-or-more times in
// `incoming`. Returns an empty slice when each key is unique. Each duplicated
// key is listed once regardless of how many times it appears.
func findDuplicateKeys(incoming []parser.Segment) []string {
	seen := make(map[string]int, len(incoming))
	for _, s := range incoming {
		seen[s.Key]++
	}
	var dups []string
	for k, n := range seen {
		if n > 1 {
			dups = append(dups, k)
		}
	}
	return dups
}
