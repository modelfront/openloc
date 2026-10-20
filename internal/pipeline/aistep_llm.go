package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"openloc/internal/ai"
	"openloc/internal/aiprompt"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/inlinetag"
	"openloc/internal/logging"
	"openloc/internal/ptr"
)

// resolveLLMPredictRunner builds the QE+APE batch runner for an engine row
// carrying a system prompt: the row's model preset supplies the
// provider (openai/claude) + model, the prompt supplies the instructions and
// the QE-vs-APE mode (out_text ⇒ APE), and the LLM is asked for our expected
// output format per segment — {status, postedit, score}.
//
// Misconfiguration and a not-connected provider are benign skips
// (PhaseSkipNoProvider + log): the step still lets the human review, predict
// just doesn't pre-approve anything. Only prompt-load failures are hard errors.
func (s *Service) resolveLLMPredictRunner(
	ctx context.Context, tenantID int64, row *db.AiEngineRow,
	stepName string, jobID int64, project db.Project, sourceLang, targetLang string,
) (runner aiPredictRunner, mode, source, model string, skip PhaseSkipReason, err error) {
	prompt, err := s.queries.GetAIPrompt(ctx, db.GetAIPromptParams{ID: *row.SystemPromptID, TenantID: tenantID})
	if err != nil {
		return nil, "", "", "", "", fmt.Errorf("get system prompt %d: %w", *row.SystemPromptID, err)
	}
	// The mode decides both halves of the exchange: which fields the contract
	// asks for, and how the answers are routed. It also names the step for the
	// user, so it is resolved before anything can fail.
	mode = aiprompt.Mode(prompt)

	benignSkip := func(reason string) (aiPredictRunner, string, string, string, PhaseSkipReason, error) {
		logging.Warn(ctx, nil, "LLM predict skipped: "+reason, "ai-step-llm-skipped", logging.JobID(jobID), logging.WorkflowStep(stepName))
		return nil, mode, "", "", PhaseSkipNoProvider, nil
	}
	if s.aiPredictor == nil || s.aiDecryptor == nil {
		return benignSkip("LLM QE+APE is not configured on this server")
	}
	// The row carries its own token/model/settings — no
	// ai_providers hop.
	if row.TokenID == nil {
		return benignSkip(fmt.Sprintf("AI engine row %d has no model configured", row.ID))
	}
	tok, err := s.queries.GetVendorToken(ctx, db.GetVendorTokenParams{ID: *row.TokenID, TenantID: tenantID})
	if err != nil {
		return benignSkip(fmt.Sprintf("load token %d failed: %v", *row.TokenID, err))
	}
	provider := strings.TrimSpace(tok.Provider)
	if !ai.CanPredict(provider) {
		return benignSkip(fmt.Sprintf("provider %q cannot run LLM QE+APE", provider))
	}
	apiKey, err := s.aiDecryptor.Decrypt(tok.ApiKeyEncrypted)
	if err != nil {
		return benignSkip(fmt.Sprintf("decrypt provider %q key failed: %v", provider, err))
	}
	// From the token's CONNECTION; "" = the vendor's public API.
	baseURL := strings.TrimSpace(tok.BaseUrl)
	model = strings.TrimSpace(row.Model)

	// Unparseable model output is handled per the row's bad-output action:
	// "ignore" leaves the segment untouched; "reject" drops it to the human;
	// "comment" rejects AND attaches the raw output as an AI segment comment.
	badOutput := "reject"
	if row.BadOutputAction != nil && strings.TrimSpace(*row.BadOutputAction) != "" {
		badOutput = strings.TrimSpace(*row.BadOutputAction)
	}

	// A step runs one language pair, so the additions resolve once here rather
	// than per batch.
	promptText := applyLanguageAdditions(
		prompt.PromptText, s.loadLanguageAdditions(ctx, prompt.ID), targetLang)

	cfg := ai.PredictConfig{
		Provider:   provider,
		Model:      model,
		APIKey:     apiKey,
		BaseURL:    baseURL,
		PromptText: promptText,
		SourceLang: sourceLang,
		TargetLang: targetLang,
		// The contract describes the block the segments actually carry.
		SegmentContext: prompt.SegmentContext,
		Settings:       ai.ParseSettings(ctx, row.Settings),
		Mode:           mode,
	}

	// Each segment's user message is generated from the context the system
	// prompt asks for; resolved once for the step rather than per batch.
	renderer := s.newSegPromptRenderer(ctx, tenantID, prompt, mode, project, targetLang)

	runner = func(ctx context.Context, batch []db.ListPreTranslatedByFileRow, batchIdx, totalBatches int) ([]aiPredictResult, error) {
		// Inline tags travel as chip labels so the model can judge and fix tag
		// placement. Encoded once and reused for both the built-in body and the
		// tenant's segment prompt.
		enc := encodeBatchTags(batch)

		// The model is told each segment's number in the file, not its
		// translation row id. A row id is an opaque five-digit number that means
		// nothing to the model and nothing to a human reading the debug capture;
		// the segment number is what the CAT editor shows, so a verdict can be
		// traced to a row on screen. It also gives the model real document order,
		// which is what the contract's "flag inconsistencies across them" asks it
		// to use. Unique per batch because `pending` is one file and one target
		// language (RunAIStep), which is what makes it safe to match on.
		inputs := make([]ai.PredictInput, len(batch))
		for i, r := range batch {
			inputs[i] = ai.PredictInput{
				ID:          segmentNumber(r),
				Source:      enc.Sources[i],
				Translation: enc.Targets[i],
			}
		}
		if renderer != nil {
			segRows := make([]segPromptRow, len(batch))
			for i, r := range batch {
				segRows[i] = segPromptRow{TranslationID: r.ID, SourceText: enc.Sources[i], TargetText: enc.Targets[i], PlainSource: r.SourceText}
			}
			for i, content := range renderer.RenderBatch(ctx, segRows) {
				inputs[i].Content = content
			}
		}
		bcfg := cfg
		bcfg.Tagged = enc.Tagged
		results, err := s.aiPredictor.PredictBatch(ctx, bcfg, inputs)
		if err != nil {
			logging.Warn(ctx, err, "LLM predict batch failed", "ai-step-llm-batch-failed", logging.JobID(jobID), logging.WorkflowStep(stepName), logging.Provider(provider), logging.Label("batch_index", strconv.Itoa(batchIdx)))
			var provErr *ai.ProviderError
			if errors.As(err, &provErr) {
				return nil, httputil.NewClientError(aiStepProviderMessage(provErr))
			}
			return nil, httputil.NewClientError(fmt.Sprintf("The AI step could not complete: %v. Check AI settings → Providers and try again.", err))
		}

		out := make([]aiPredictResult, 0, len(results))
		for i, r := range results {
			// Back to the translation id the moment the reply is in hand: the
			// segment number is a wire detail, and everything downstream of this
			// runner addresses rows by id.
			translationID := strconv.FormatInt(batch[i].ID, 10)
			if r.Invalid {
				if badOutput == "ignore" {
					logging.Warn(ctx, nil, "bad LLM output ignored", "ai-step-llm-bad-output", logging.JobID(jobID), logging.IDFromString(logging.KeyTranslationID, translationID), logging.Label("bad_output_action", "ignore"))
					continue
				}
				logging.Warn(ctx, nil, "bad LLM output rejected", "ai-step-llm-bad-output", logging.JobID(jobID), logging.IDFromString(logging.KeyTranslationID, translationID), logging.Label("bad_output_action", badOutput))
				rejected := aiPredictResult{predictVerdict: predictVerdict{ID: translationID, Approved: false}}
				if badOutput == "comment" {
					c := fmt.Sprintf("AI returned unparseable output: %.500s", strings.TrimSpace(r.Raw))
					rejected.Comment = &c
				}
				out = append(out, rejected)
				continue
			}
			mapped := mapLLMPredictResult(
				r, mode, *batch[i].ActiveText, ptr.Deref(batch[i].ActiveRichText), enc.Tables[i])
			mapped.ID = translationID
			out = append(out, mapped)
		}
		return out, nil
	}
	return runner, mode, provider, model, "", nil
}

// mapLLMPredictResult normalizes one LLM result to the verdict shape the
// shared routing understands. What it may set depends on the prompt's
// output mode, because that is what the model was asked for.
//
// QE+APE judges and post-edits:
//   - original approved → approved outcome (auto-approved, text unchanged);
//     qe_note = score;
//   - not approved + approved postedit that changes the text → Postedit +
//     PosteditApproved=true → ape_approved (applied, auto-approved);
//     qe_note = postedit_score (the score of what's now in the segment);
//   - otherwise → rejected outcome; an unapproved postedit is discarded;
//     qe_note = score (the original's).
//
// APE post-edits and does not judge — it stores out_status = false, so it never
// approves. A postedit that changes the text is applied with PosteditApproved
// left nil, which routes to ape_needs_review: the fix lands in the segment and
// a human reviews it. No postedit (or one that changes nothing) → rejected
// outcome, original text, same human review. qe_note = score either way, which
// for this mode describes the text that ends up in the segment.
//
// QE rows are instructed to return no postedit; if a model returns one anyway
// it is ignored here (and routeQEOutcome would ignore it regardless). The
// comment becomes an AI-authored segment comment.
// tbl is the row's chip table, nil for a segment with no inline tags. The
// postedit comes back chip-encoded, so it is decoded here: the rich form goes
// to active_rich_text and its tag-stripped plain form to active_text.
func mapLLMPredictResult(r ai.PredictResult, mode string, currentText, currentRichText string, tbl *inlinetag.Table) aiPredictResult {
	mapped := predictVerdict{ID: r.ID}
	score := r.Score
	res := aiPredictResult{}

	var plain, rich string
	var unknown []string
	if r.Postedit != nil {
		plain, rich, unknown = decodePostedit(*r.Postedit, tbl)
	}

	// "Did the postedit change anything" has to be asked of the RICH form on a
	// tagged segment. Moving a tag pair off the words it wrongly covered leaves
	// every word where it was, so the tag-stripped forms are identical and a
	// plain comparison reads the correction as a no-op — discarding exactly the
	// fix inline tags were sent for.
	changed := plain != currentText
	if !tbl.Empty() {
		changed = rich != currentRichText
	}
	applyPostedit := func() {
		mapped.Postedit = &plain
		if !tbl.Empty() {
			res.PosteditRichText = &rich
		}
		res.UnknownTags = unknown
	}

	switch {
	case mode == ai.ModeAPE:
		// Every fix is applied, nothing is approved and nothing is scored.
		// mapped.Approved stays false and PosteditApproved stays nil —
		// routePredictOutcome reads a non-nil true there as "auto-approve",
		// which is the one thing this mode must never do. The score goes the
		// same way: the contract does not ask for one, and a model that
		// volunteers it anyway must not have it recorded as a verdict.
		score = nil
		if r.Postedit != nil && changed {
			applyPostedit()
		}
	case mode != ai.ModeQE && !r.Approved && r.Postedit != nil && changed &&
		r.PosteditApproved != nil && *r.PosteditApproved:
		approved := true
		applyPostedit()
		mapped.PosteditApproved = &approved
		// The postedit is what lands in the segment — keep ITS score.
		score = r.PosteditScore
	default:
		mapped.Approved = r.Approved
	}
	res.predictVerdict = mapped
	res.Comment = r.Comment
	if score != nil {
		note := strconv.FormatFloat(*score, 'f', -1, 64)
		res.Note = &note
	}
	return res
}

// segmentNumber is the segment's place in the file as the editor shows it.
// segments.position is 0-based; the editor numbers from 1, and the number in a
// prompt has to be the one a person can look up.
func segmentNumber(r db.ListPreTranslatedByFileRow) string {
	return strconv.Itoa(int(r.Position) + 1)
}
