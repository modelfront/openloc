package aiprompt

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// The read shape: a prompt with its per-language additions nested, so the
// editor loads a prompt in one request and never has to ask a second time to
// know what it will actually send.

// promptResponse is one prompt as the API returns it.
type promptResponse struct {
	db.AiPrompt
	// LanguageAdditions is never null — an empty list is the common case and a
	// client should not have to handle both spellings of "none".
	LanguageAdditions []LanguageAddition `json:"language_additions"`
}

func toResponse(p db.AiPrompt, additions []LanguageAddition) promptResponse {
	if additions == nil {
		additions = []LanguageAddition{}
	}
	return promptResponse{AiPrompt: p, LanguageAdditions: additions}
}

// listResponse nests each tenant's additions onto its prompts with one extra
// query for the whole page, rather than one per prompt.
func listResponse(ctx context.Context, q db.Querier, prompts []db.AiPrompt) []promptResponse {
	byPrompt, err := AdditionsOf(ctx, q, tenantOf(prompts))
	if err != nil {
		// Best-effort: a prompt list that renders without its additions is far
		// better than a page that fails to load. The editor shows none, and a
		// save from that state would drop them — so this is logged loudly
		// rather than passed over.
		logging.Warn(ctx, err, "could not load per-language prompt additions for the list",
			"prompt.language_additions_list_failed")
	}
	out := make([]promptResponse, len(prompts))
	for i, p := range prompts {
		out[i] = toResponse(p, byPrompt[p.ID])
	}
	return out
}

// tenantOf reads the tenant off the loaded prompts, which are already
// tenant-scoped by the query that produced them. Zero for an empty list — the
// additions query then matches nothing, which is the right answer.
func tenantOf(prompts []db.AiPrompt) int64 {
	if len(prompts) == 0 {
		return 0
	}
	return prompts[0].TenantID
}
