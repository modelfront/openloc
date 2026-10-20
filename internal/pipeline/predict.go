package pipeline

import (
	db "openloc/internal/database/sqlcgen"
)

// routePredictOutcome maps a QE+APE verdict to a translation update
// for an APE step. APE applies post-edits and can auto-approve either
// the original MT or the APE rewrite. A postedit always replaces the target,
// and then postedit_approved alone decides approval. The 4 outcomes:
//  1. no postedit + approved=true → auto_approved (MT accepted as-is)
//  2. postedit + postedit_approved=true → APE rewrite auto_approved
//  3. postedit + postedit_approved≠true → APE suggestion, human reviews
//  4. no postedit + approved=false → rejected, human reviews the original MT
func routePredictOutcome(row db.ListPreTranslatedByFileRow, result aiPredictResult) db.UpdateTranslationPredictParams {
	params := db.UpdateTranslationPredictParams{
		ID: row.ID,
	}

	if result.Postedit != nil {
		// The postedit becomes active_text (history keeps it as kind='ape').
		params.ActiveText = result.Postedit
		params.ActiveRichText = posteditRichText(row, result)

		if result.PosteditApproved != nil && *result.PosteditApproved {
			// Outcome 2: APE fixed it, auto-approve
			params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApeApproved, Valid: true}
			params.Status = db.TranslationStatusAutoApproved
		} else {
			// Outcome 3: APE suggested but not confident, human reviews
			params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApeNeedsReview, Valid: true}
			params.Status = db.TranslationStatusNeedsReview
		}
	} else if result.Approved {
		// Outcome 1: MT approved, auto-approve
		params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true}
		params.ActiveText = row.ActiveText
		params.ActiveRichText = row.ActiveRichText
		params.Status = db.TranslationStatusAutoApproved
	} else {
		// Outcome 4: Rejected, no APE fix, human reviews original MT
		params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeRejected, Valid: true}
		params.ActiveText = row.ActiveText
		params.ActiveRichText = row.ActiveRichText
		params.Status = db.TranslationStatusNeedsReview
	}

	return params
}

// posteditRichText resolves what a postedit writes to active_rich_text.
//
// The editor and the export both prefer the rich column, so a postedit that
// updates only active_text ships the pre-APE target. Nil is not a safe default
// either: the query COALESCEs it away to keep the existing value, which on a
// tagged row is exactly that stale target. So a row that HAS rich text always
// gets a non-nil answer — the decoded postedit when the model kept its chips,
// the plain postedit when it dropped them.
func posteditRichText(row db.ListPreTranslatedByFileRow, result aiPredictResult) *string {
	if result.PosteditRichText != nil {
		return result.PosteditRichText
	}
	if row.ActiveRichText == nil {
		return nil // untagged row — nothing to keep in sync
	}
	return result.Postedit
}

// routeQEOutcome maps a verdict for a QE step. QE only
// verifies — it never applies a post-edit. approved=true → auto_approved with
// the MT text unchanged; anything else → needs_review (the human reviews the
// original MT). Any postedit the backend returns is deliberately ignored for QE.
func routeQEOutcome(row db.ListPreTranslatedByFileRow, result aiPredictResult) db.UpdateTranslationPredictParams {
	params := db.UpdateTranslationPredictParams{
		ID:             row.ID,
		ActiveText:     row.ActiveText,
		ActiveRichText: row.ActiveRichText,
	}
	if result.Approved {
		params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true}
		params.Status = db.TranslationStatusAutoApproved
	} else {
		params.PredictOutcome = db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeRejected, Valid: true}
		params.Status = db.TranslationStatusNeedsReview
	}
	return params
}
