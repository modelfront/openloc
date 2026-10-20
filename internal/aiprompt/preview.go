package aiprompt

import (
	"encoding/json"
	"net/http"
	"strings"

	"openloc/internal/ai"
	"openloc/internal/httputil"
)

// Prompt preview. The editor shows the author the whole system prompt — their
// own instruction text plus the output contract the platform appends — by
// asking the server to assemble it, rather than by
// re-implementing the appended half in TypeScript.
//
// Read-only and stateless: it previews whatever is currently typed, which is
// why it takes a draft body instead of a prompt id.

// previewLangs are the stand-ins used when the caller has no concrete job in
// hand (the prompt editor never does — a prompt is reused across language
// pairs). They render as the same `<token>` form the instruction text uses for
// its own placeholders, so the shape reads as a template rather than as a claim
// about some particular job.
const (
	previewSourceLang = "<source_lang>"
	previewTargetLang = "<target_lang>"
)

// PreviewInput is a prompt draft. The output flags decide the mode, exactly as
// they do for a stored row.
type PreviewInput struct {
	Provider    string `json:"provider"`
	PromptText  string `json:"prompt_text"`
	OutStatus   bool   `json:"out_status"`
	OutText     bool   `json:"out_text"`
	OutNote     bool   `json:"out_note"`
	OutComments bool   `json:"out_comments"`
	// SegmentContext is the optional per-segment context the draft has
	// switched on. The contract names exactly these, so the preview is wrong
	// without it.
	SegmentContext []string `json:"segment_context"`
	// Optional concrete languages. Empty falls back to the markers above.
	SourceLang string `json:"source_lang"`
	TargetLang string `json:"target_lang"`
	// Optional model id. A prompt is model-independent, so the prompt editor
	// has none; an AI engine row does, and passing it answers whether that
	// model has its output shape enforced by the provider.
	Model string `json:"model"`
}

// PreviewResponse carries the assembled prompt plus the mode it was assembled
// for, so the client can label the panel without re-deriving it.
type PreviewResponse struct {
	ai.PromptPreview
	ContextPreview json.RawMessage `json:"context_preview"`
	Mode           string          `json:"mode"`
	// SchemaEnforced is true when the given model accepts a JSON schema, so the
	// reply shape is guaranteed rather than requested. True with no model in
	// hand too: the shape follows from the mode, so a schema is assumed until a
	// model is known not to take one.
	SchemaEnforced bool `json:"schema_enforced"`
	// OutputSchema is the shape a reply must satisfy. It follows from the mode
	// alone — both vendors receive the same schema, differing only in the
	// request field it travels in — so a caller with no model still sees it.
	// Absent for a model that takes no schema: there the prose is the contract.
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// Preview assembles a draft prompt: the author's instruction text plus the
// language direction and output contract the platform appends. Shared by the
// REST route and the MCP tool.
func Preview(in PreviewInput) (PreviewResponse, error) {
	mode := ModeForFlags(in.OutStatus, in.OutText, in.OutComments)
	src := orDefault(in.SourceLang, previewSourceLang)
	tgt := orDefault(in.TargetLang, previewTargetLang)
	// The vendor follows from the model id (a pure lookup), so a caller that
	// knows its model needn't also tell us whose it is.
	model := ai.PreviewModel(strings.TrimSpace(in.Provider), strings.TrimSpace(in.Model))
	resp := PreviewResponse{
		PromptPreview: ai.AssemblePromptWithContext(
			mode, in.PromptText, src, tgt, model, normalizeSegmentContext(in.SegmentContext)),
		Mode: mode,
	}
	// The schema follows from the mode, so a prompt editor with no model in hand
	// can still show the shape a reply must satisfy. A model that is KNOWN not
	// to take one is the exception: there the prose contract is what is sent,
	// and showing a schema we never send would be worse than showing none.
	resp.SchemaEnforced = ai.SchemaEnforced(ai.ProviderForModelID(model), model)
	if resp.SchemaEnforced {
		resp.OutputSchema = ai.ResultsSchema(mode)
	}
	var err error
	resp.ContextPreview, err = ai.PreviewContext(mode, normalizeSegmentContext(in.SegmentContext))
	if err != nil {
		return PreviewResponse{}, err
	}
	return resp, nil
}

// HandlePreview assembles a draft prompt. Manager-read: it reads nothing but
// the request body and touches no tenant data, and the people who pick prompts
// are the people who need to see what they send.
func (h *Handler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	var in PreviewInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	resp, err := Preview(in)
	if err != nil {
		httputil.ServerError(w, "preview", "segment context")
		return
	}
	httputil.Data(w, resp)
}

func orDefault(v, fallback string) string {
	if t := strings.TrimSpace(v); t != "" {
		return t
	}
	return fallback
}
