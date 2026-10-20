package aiprompt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/segmentprompt"
)

// Transport-free creation, shared by the REST handler and the MCP tool
// (a second surface calls the same service rather
// than repeating the validation, so the two cannot drift).

// Input is a prompt's writable shape. A create always sends the whole thing —
// there are no partial writes.
type Input struct {
	Name        string
	PromptText  string
	OutStatus   bool
	OutText     bool
	OutNote     bool
	OutComments bool
	// SegmentContext is the OPTIONAL per-segment context this prompt sends
	// (segmentprompt.Optional). The source text and, for a reviewing mode, the
	// translation are implied by the output mode rather than chosen.
	SegmentContext []string
	// LanguageAdditions is the full set, in the order the author arranged them
	// (replace-all — see DeleteAIPromptLanguageAdditions).
	LanguageAdditions []LanguageAddition
}

// LanguageAddition is extra instruction text that applies only to jobs into
// some target languages. At least one must be set (see languages.go).
type LanguageAddition struct {
	TargetLanguages []string `json:"target_languages"`
	Text            string   `json:"text"`
}

// ValidationError carries a message that is safe to show the caller.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(msg string) error { return &ValidationError{Msg: msg} }

// Validate normalizes and checks an Input in place. A nil return means valid.
func Validate(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	in.PromptText = strings.TrimSpace(in.PromptText)
	if in.Name == "" {
		return invalid("name is required")
	}
	if in.PromptText == "" {
		return invalid("prompt_text is required")
	}
	if !in.OutStatus && !in.OutText && !in.OutNote && !in.OutComments {
		return invalid("at least one output format is required")
	}
	in.SegmentContext = normalizeSegmentContext(in.SegmentContext)
	return validateLanguageAdditions(in)
}

// normalizeSegmentContext keeps only recognised keys, in canonical order.
//
// Dropped rather than rejected: the set is a UI convenience, and a client
// sending a retired key should still be able to save. A key the generator does not understand would
// reach the model as nothing at all, so storing it would only make the prompt
// page lie about what gets sent.
func normalizeSegmentContext(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, known := range segmentprompt.Optional {
		for _, k := range keys {
			if strings.TrimSpace(k) == known {
				out = append(out, known)
				break
			}
		}
	}
	return out
}

// validateLanguageAdditions normalizes the additions in place. A row that is
// entirely blank is dropped — that is someone adding a row and changing their
// mind — but a partly-filled one is rejected, because each half alone is a rule
// that silently does nothing: text with no language would match every job, and
// a language with no text appends nothing.
func validateLanguageAdditions(in *Input) error {
	out := make([]LanguageAddition, 0, len(in.LanguageAdditions))
	for _, a := range in.LanguageAdditions {
		tgt := NormalizeLanguages(a.TargetLanguages)
		text := strings.TrimSpace(a.Text)
		switch {
		case len(tgt) == 0 && text == "":
			continue
		case len(tgt) == 0:
			return invalid("each language addition needs a target language")
		case text == "":
			return invalid("each language addition needs some text")
		}
		out = append(out, LanguageAddition{TargetLanguages: tgt, Text: text})
	}
	in.LanguageAdditions = out
	return nil
}

// writeLanguageAdditions replaces a prompt's additions with in's. Position is
// the submitted order, so the author's arrangement is what the model reads.
//
// q must be transactional when the caller also wrote the prompt row: a delete
// that lands without its re-insert leaves a prompt silently missing its
// per-language rules.
func writeLanguageAdditions(ctx context.Context, q db.Querier, promptID int64, additions []LanguageAddition) error {
	if err := q.DeleteAIPromptLanguageAdditions(ctx, promptID); err != nil {
		return err
	}
	for i, a := range additions {
		if err := q.CreateAIPromptLanguageAddition(ctx, db.CreateAIPromptLanguageAdditionParams{
			PromptID:        promptID,
			TargetLanguages: a.TargetLanguages,
			AdditionText:    a.Text,
			Position:        int32(i),
		}); err != nil {
			return err
		}
	}
	return nil
}

// Create validates and inserts a prompt. A duplicate name comes back as a
// ValidationError rather than an opaque database error — UNIQUE(tenant_id,
// name) is reachable by simply duplicating a default twice without renaming.
func Create(ctx context.Context, q db.Querier, tenantID int64, in Input) (db.AiPrompt, error) {
	if err := Validate(&in); err != nil {
		return db.AiPrompt{}, err
	}
	prompt, err := q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
		TenantID:       tenantID,
		Name:           in.Name,
		PromptText:     in.PromptText,
		OutStatus:      in.OutStatus,
		OutText:        in.OutText,
		OutNote:        in.OutNote,
		OutComments:    in.OutComments,
		SegmentContext: in.SegmentContext,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.AiPrompt{}, invalid("a prompt with this name already exists — pick another name")
		}
		return db.AiPrompt{}, err
	}
	if err := writeLanguageAdditions(ctx, q, prompt.ID, in.LanguageAdditions); err != nil {
		return db.AiPrompt{}, err
	}
	return prompt, nil
}

// Update rewrites a prompt and replaces its language additions. Validation and
// the read-only-default guard belong to the caller — the handler already owns
// the tenant-scoped existence check that precedes both.
//
// q must be transactional: the row update and the additions replace are one
// change, and half of it applied is a prompt whose rules no longer match its
// text.
func Update(ctx context.Context, q db.Querier, tenantID, id int64, in Input) (db.AiPrompt, error) {
	prompt, err := q.UpdateAIPrompt(ctx, db.UpdateAIPromptParams{
		ID:             id,
		TenantID:       tenantID,
		Name:           in.Name,
		PromptText:     in.PromptText,
		OutStatus:      in.OutStatus,
		OutText:        in.OutText,
		OutNote:        in.OutNote,
		OutComments:    in.OutComments,
		SegmentContext: in.SegmentContext,
	})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return db.AiPrompt{}, invalid("a prompt with this name already exists — pick another name")
		}
		return db.AiPrompt{}, err
	}
	if err := writeLanguageAdditions(ctx, q, id, in.LanguageAdditions); err != nil {
		return db.AiPrompt{}, err
	}
	return prompt, nil
}

// AsValidation reports whether err is a ValidationError and returns its
// message, so a caller can map it onto its own transport's 4xx.
func AsValidation(err error) (string, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v.Msg, true
	}
	return "", false
}

// Mode is a prompt's output mode, derived from its flags: the single choice a
// user makes in the UI, which the four booleans encode. Mirrors flagsToMode in
// web/src/components/ai/output-mode.ts. APE and MT both emit text with no
// status; the comment flag tells them apart.
func Mode(p db.AiPrompt) string {
	return ModeForFlags(p.OutStatus, p.OutText, p.OutComments)
}

// ModeForFlags is Mode for a draft that isn't a stored row yet — the prompt
// preview resolves the mode of whatever is currently typed into the editor.
func ModeForFlags(outStatus, outText, outComments bool) string {
	switch {
	case outStatus && outText:
		return "qe_ape"
	case outStatus:
		return "qe"
	case outComments:
		return "ape"
	default:
		return "mt"
	}
}

// FlagsForMode is the inverse of Mode: the output flags a named mode implies.
// Lets a caller say "an APE prompt" instead of setting four booleans by hand.
// Mirrors MODE_FLAGS in web/src/components/ai/output-mode.ts.
func FlagsForMode(mode string) (Input, bool) {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "qe":
		return Input{OutStatus: true, OutNote: true, OutComments: true}, true
	case "qe_ape":
		return Input{OutStatus: true, OutText: true, OutNote: true, OutComments: true}, true
	case "ape":
		return Input{OutText: true, OutNote: true, OutComments: true}, true
	case "mt":
		return Input{OutText: true, OutNote: true}, true
	}
	return Input{}, false
}

// CheckDelete returns the prompt when it may be deleted. Built-in prompts
// and prompts an AI engine still runs are refused with
// httputil.ConflictError; the engine FK refuses the delete too, so the check
// turns an opaque failure into a reason.
func CheckDelete(ctx context.Context, q db.Querier, tenantID, id int64) (db.AiPrompt, error) {
	prompt, err := q.GetAIPrompt(ctx, db.GetAIPromptParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.AiPrompt{}, err
	}
	if prompt.IsDefault {
		return db.AiPrompt{}, httputil.NewConflictError("default prompts cannot be deleted")
	}
	refs, err := q.CountAIEnginesForPrompt(ctx, &id)
	if err != nil {
		return db.AiPrompt{}, fmt.Errorf("count AI engines for prompt: %w", err)
	}
	if refs > 0 {
		return db.AiPrompt{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d AI engine row(s) still use this prompt", refs))
	}
	return prompt, nil
}

// Delete removes a prompt after CheckDelete.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := CheckDelete(ctx, q, tenantID, id); err != nil {
		return err
	}
	return q.DeleteAIPrompt(ctx, db.DeleteAIPromptParams{ID: id, TenantID: tenantID})
}
