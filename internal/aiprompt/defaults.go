package aiprompt

import (
	"context"

	db "openloc/internal/database/sqlcgen"
)

// Built-in default prompts: read-only, undeletable templates so a tenant can
// build engines without writing a prompt first. Provisioned lazily by
// EnsureDefaultPrompts (called from HandleList — the AI page is the only place
// prompts are picked). Output flags per mode mirror MODE_FLAGS in
// web/src/components/ai/output-mode.ts (keep in sync).
//
// Each names the language pair through ai.TokenSourceLang / ai.TokenTargetLang,
// which are resolved at send time.

// defaultPrompt is one built-in template.
type defaultPrompt struct {
	Name        string
	PromptText  string
	OutStatus   bool
	OutText     bool
	OutNote     bool
	OutComments bool
	// SegmentContext is the optional per-segment context the template ships
	// with.
	SegmentContext []string
}

// DefaultPrompts are the stock templates. Display order is enforced in the FE
// by DEFAULT_MODE_ORDER; the slice order here only affects provisioning.
var DefaultPrompts = []defaultPrompt{
	{
		// Plain "Default" — the MT engines page lists this one alone, and the card
		// carries an MT mode badge.
		Name: "Default",
		PromptText: "You are a professional <source_lang>→<target_lang> translator. Translate the source segment into " +
			"<target_lang> faithfully and idiomatically, preserving meaning, tone, formatting tags, and " +
			"placeholders." +
			"\n\n" +
			"For each segment return \"target\" with the translation.",
		OutText: true, OutNote: true,
	},
}

// EnsureDefaultPrompts provisions the built-in templates for a tenant and
// keeps them current: defaults are read-only, so a code-shipped wording
// change safely refreshes existing rows (the conditional DO UPDATE only
// touches is_default rows that drifted — a user's same-named custom prompt
// always wins). Idempotent; one no-op upsert per default in the steady state.
func EnsureDefaultPrompts(ctx context.Context, q db.Querier, tenantID int64) error {
	for _, p := range DefaultPrompts {
		if err := q.UpsertDefaultAIPrompt(ctx, db.UpsertDefaultAIPromptParams{
			TenantID:       tenantID,
			Name:           p.Name,
			PromptText:     p.PromptText,
			OutStatus:      p.OutStatus,
			OutText:        p.OutText,
			OutNote:        p.OutNote,
			OutComments:    p.OutComments,
			SegmentContext: p.SegmentContext,
		}); err != nil {
			return err
		}
	}
	return nil
}
