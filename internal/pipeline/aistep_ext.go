package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/extension"
	"openloc/internal/httputil"
	"openloc/internal/inlinetag"
	"openloc/internal/logging"
	"openloc/pkg/extsdk"
)

// resolveExtensionPredictor loads the extension behind the row's key. A
// removed extension fails the step with an actionable reason; a server with
// no extension runtime skips it.
func (s *Service) resolveExtensionPredictor(ctx context.Context, tenantID int64, engineName string, tokenID int64) (extension.BatchPredictor, PhaseSkipReason, error) {
	if s.extensions == nil {
		return nil, PhaseSkipNoExtensions, nil
	}
	pred, err := s.extensions.PredictorForToken(ctx, tenantID, tokenID)
	if errors.Is(err, extension.ErrNotInstalled) {
		return nil, "", aiStepUnusable(&EngineUnusableError{EngineName: engineName, Reason: enginehealth.NoExtension})
	}
	if errors.Is(err, extension.ErrKeyUnreadable) {
		return nil, "", Permanent(httputil.NewClientError(fmt.Sprintf(
			"The AI step could not complete: the API key of the engine %q cannot be read. Enter the key again in AI settings.", engineName)))
	}
	if errors.Is(err, extension.ErrCannotReview) {
		return nil, "", Permanent(httputil.NewClientError(fmt.Sprintf(
			"The AI step could not complete: the custom provider of the engine %q cannot review translations.", engineName)))
	}
	if errors.Is(err, extension.ErrNotConfigured) {
		return nil, "", Permanent(httputil.NewClientError(
			"The AI step could not complete: custom providers are not available on this server — ask your administrator to set AI_SECRETS_KEY."))
	}
	if err != nil {
		return nil, "", fmt.Errorf("load extension for key %d: %w", tokenID, err)
	}
	return pred, "", nil
}

// extensionPredictRunner wraps an extension's predict as an aiPredictRunner.
// Inline tags travel as chip placeholders, same as the LLM path, and a
// postedit is decoded back to markup.
func (s *Service) extensionPredictRunner(pred extension.BatchPredictor, file db.File, project db.Project, stepName string, jobID int64, sourceLang, targetLang string) aiPredictRunner {
	name := pred.Name()
	return func(ctx context.Context, batch []db.ListPreTranslatedByFileRow, batchIdx, totalBatches int) ([]aiPredictResult, error) {
		enc := encodeBatchTags(batch)
		rows := make([]extsdk.PredictRow, len(batch))
		tableByID := make(map[string]*inlinetag.Table, len(batch))
		for i, row := range batch {
			id := strconv.FormatInt(row.ID, 10)
			rows[i] = extsdk.PredictRow{ID: id, Source: enc.Sources[i], Target: enc.Targets[i], Origin: extensionOrigin(row.Origin)}
			// Keyed by id, not position: a reordered response must not decode a
			// postedit against another segment's tag table.
			tableByID[id] = enc.Tables[i]
		}
		meta := extsdk.PredictMeta{
			FileID: strconv.FormatInt(file.ID, 10), FileName: file.Name,
			ProjectID: strconv.FormatInt(project.ID, 10), ProjectName: project.Name,
			StepName: stepName, BatchIndex: batchIdx, BatchCount: totalBatches,
		}
		resp, err := pred.Predict(ctx, sourceLang, targetLang, meta, rows)
		if err != nil {
			logging.Warn(ctx, err, "extension batch failed", "ai-step-extension-batch-failed",
				logging.JobID(jobID), logging.WorkflowStep(stepName), logging.Label("extension", pred.Provider()),
				logging.Label("batch_index", strconv.Itoa(batchIdx)))
			if errors.Is(err, extension.ErrModuleDamaged) {
				return nil, Permanent(httputil.NewClientError(fmt.Sprintf(
					"The AI step could not complete: the stored file of %s is damaged. Add the custom provider again in AI settings → Providers.", name)))
			}
			var ce *extension.CallError
			if errors.As(err, &ce) {
				// The latest vendor request got no answer, the vendor failed, or
				// it answered and the extension still failed: an outage, which
				// may be our integration. Checked first: an earlier 4xx in the
				// same call does not make an unanswered retry the user's.
				if ce.VendorUnreachable || ce.VendorStatus >= 500 || (ce.VendorStatus != 0 && ce.VendorStatus < 400) {
					kind := ai.ProviderErrProvider
					if ce.VendorUnreachable {
						kind = ai.ProviderErrUnreachable
					}
					return nil, &VendorOutageError{
						Message: fmt.Sprintf("The AI step could not complete: %s: %s. Try again in a few minutes.", name, ce.Msg),
						Err:     &ai.ProviderError{Provider: pred.Provider(), Status: ce.VendorStatus, Kind: kind},
					}
				}
				// A vendor 4xx is the user's key, plan or rate: theirs to fix.
				// The extension's own message, with the token already removed.
				if ce.VendorStatus >= 400 {
					cerr := httputil.NewClientError(fmt.Sprintf("The AI step could not complete: %s: %s. Check the %s key in AI settings and try again.", name, ce.Msg, name))
					// The vendor refused the request itself; a retry sends the
					// same request. 429 asks to come back later, so it is retried.
					if ce.VendorStatus != http.StatusTooManyRequests {
						return nil, Permanent(cerr)
					}
					return nil, cerr
				}
				// No vendor answer behind it: the extension refused on its
				// own, crashed or returned bad output. It may be its own
				// fault, so it is reported.
				return nil, &ExtensionFailure{Message: fmt.Sprintf("The AI step could not complete: %s: %s.", name, ce.Msg), Provider: pred.Provider()}
			}
			// The host failed to run the extension (a timeout, a missing export).
			return nil, &ExtensionFailure{Message: fmt.Sprintf("The AI step could not complete: %s did not respond. Try again.", name), Provider: pred.Provider(), Err: err}
		}
		out := make([]aiPredictResult, 0, len(resp.Rows))
		for _, r := range resp.Rows {
			tbl, ok := tableByID[r.ID]
			if !ok {
				continue // not a row of this batch
			}
			res := aiPredictResult{
				predictVerdict: predictVerdict{ID: r.ID, Approved: r.Approved, PosteditApproved: r.PosteditApproved},
				Comment:        r.Comment,
			}
			if r.Score != nil {
				note := strconv.Itoa(*r.Score)
				res.Note = &note
			}
			if r.Postedit != nil {
				plain, rich, unknown := decodePostedit(*r.Postedit, tbl)
				res.Postedit = &plain
				res.UnknownTags = unknown
				if !tbl.Empty() {
					res.PosteditRichText = &rich
				}
			}
			out = append(out, res)
		}
		return out, nil
	}
}

// extensionOrigin is the contract's origin for a row: "tm" for a memory
// match, "mt" for everything else.
func extensionOrigin(o db.NullTranslationOrigin) string {
	if o.Valid && o.TranslationOrigin == db.TranslationOriginTm {
		return "tm"
	}
	return "mt"
}
