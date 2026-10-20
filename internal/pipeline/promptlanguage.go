package pipeline

import (
	"context"

	"openloc/internal/aiprompt"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// Per-language prompt additions on the execution path. A system prompt carries
// extra instructions that only apply to some languages; the matching ones are
// appended to the instruction text before it becomes the system message.
//
// Shared by both LLM paths — QE+APE (aistep_llm.go) and AI-as-MT
// (resolveAIMTEngine) — so a job gets the same prompt whichever one runs it.
//
// The load is separate from the apply because the two happen at different
// rates: the additions are fetched once per run, and applied per request on the
// MT path, where each batch carries its own language pair.

// loadLanguageAdditions fetches a prompt's additions in append order.
//
// Best-effort: a failed read yields none, so the job runs on the base prompt
// rather than failing outright. That trades a quality regression for an outage,
// which is the right way round here — but it is silent to the tenant, so it is
// logged as a warning rather than swallowed.
func (s *Service) loadLanguageAdditions(ctx context.Context, promptID int64) []db.AiPromptLanguageAddition {
	additions, err := s.queries.ListAIPromptLanguageAdditionsForPrompt(ctx, promptID)
	if err != nil {
		logging.Warn(ctx, err, "could not load per-language prompt additions — running on the base prompt",
			"prompt.language_additions_load_failed")
		return nil
	}
	return additions
}

// applyLanguageAdditions is aiprompt.ApplyLanguageAdditions, named here so both
// call sites read the same and neither reaches past the pipeline for it.
func applyLanguageAdditions(base string, additions []db.AiPromptLanguageAddition, targetLang string) string {
	return aiprompt.ApplyLanguageAdditions(base, additions, targetLang)
}
