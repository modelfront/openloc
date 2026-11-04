package pipeline

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// addAIComment writes a result's comment as an AI-authored segment comment
// (NULL user_id) and reports whether a new one was added. A live AI comment
// with the same wording on the translation is not repeated, which is what
// keeps a re-run over unchanged segments from stacking duplicates.
//
// Best-effort, like history emission: a comment failure must not fail the step.
func (s *Service) addAIComment(ctx context.Context, jobID, translationID int64, result aiPredictResult) bool {
	comment := aiComment(result)
	if comment == nil {
		return false
	}
	n, err := s.queries.CreateAICommentUnlessDuplicate(ctx, db.CreateAICommentUnlessDuplicateParams{
		TranslationID: translationID,
		Content:       *comment,
	})
	if err != nil {
		logging.Error(ctx, err, "failed to create AI comment", "ai-step-comment-failed", logging.JobID(jobID), logging.TranslationID(translationID))
		return false
	}
	return n > 0
}
