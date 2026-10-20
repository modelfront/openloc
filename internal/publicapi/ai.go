package publicapi

import (
	"net/http"
	"strings"

	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// AIPrompt is a v1 system prompt for AI engines. mode is what the engine
// returns with it: qe (a quality verdict), qe_ape (a verdict and a fix),
// ape (a fix) or mt (a translation).
type AIPrompt struct {
	ID                int64                       `json:"id"`
	Name              string                      `json:"name"`
	Mode              string                      `json:"mode"`
	PromptText        string                      `json:"prompt_text"`
	SegmentContext    []string                    `json:"segment_context"`
	LanguageAdditions []aiprompt.LanguageAddition `json:"language_additions"`
	BuiltIn           bool                        `json:"built_in"`
	CreatedAt         string                      `json:"created_at"`
	UpdatedAt         string                      `json:"updated_at"`
}

func aiPromptDTO(p db.AiPrompt, additions []aiprompt.LanguageAddition) AIPrompt {
	ctx := p.SegmentContext
	if ctx == nil {
		ctx = []string{}
	}
	if additions == nil {
		additions = []aiprompt.LanguageAddition{}
	}
	return AIPrompt{ID: p.ID, Name: p.Name, Mode: aiprompt.Mode(p), PromptText: p.PromptText, SegmentContext: ctx,
		LanguageAdditions: additions, BuiltIn: p.IsDefault, CreatedAt: timestamp(p.CreatedAt), UpdatedAt: timestamp(p.UpdatedAt)}
}

// AIEngine is a v1 AI engine: one provider row that names the connection
// (token_id), the model and, for an LLM, the system prompt. The provider key
// itself is never returned.
type AIEngine struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	Purpose   string        `json:"purpose"`
	Enabled   bool          `json:"enabled"`
	Rows      []AIEngineRow `json:"rows"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

// AIEngineRow is one provider row of an engine.
type AIEngineRow struct {
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	TokenID         *int64   `json:"token_id"`
	Model           string   `json:"model"`
	SystemPromptID  *int64   `json:"system_prompt_id"`
	BadOutputAction *string  `json:"bad_output_action"`
}

func aiEngineDTO(e db.AiEngine, rows []db.AiEngineRow) AIEngine {
	out := AIEngine{ID: e.ID, Name: e.Name, Purpose: e.Purpose, Enabled: e.Enabled, Rows: make([]AIEngineRow, len(rows)),
		CreatedAt: timestamp(e.CreatedAt), UpdatedAt: timestamp(e.UpdatedAt)}
	for i, r := range rows {
		targets := r.TargetLangs
		if targets == nil {
			targets = []string{}
		}
		out.Rows[i] = AIEngineRow{SourceLanguage: r.SourceLang, TargetLanguages: targets, TokenID: r.TokenID, Model: r.Model,
			SystemPromptID: r.SystemPromptID, BadOutputAction: r.BadOutputAction}
	}
	return out
}

// writeAIError maps an AI prompt or engine error: validation is a 400.
func writeAIError(w http.ResponseWriter, err error, verb, resource string) {
	if msg, ok := aiprompt.AsValidation(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	if msg, ok := aiengine.AsValidation(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	httputil.ServiceError(w, err, verb, resource)
}

// --- Prompts ---

func (h Handlers) listAIPrompts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	prompts, err := h.Queries.ListAIPrompts(ctx, tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI prompts")
		return
	}
	additions, err := aiprompt.AdditionsOf(ctx, h.Queries, tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI prompts")
		return
	}
	writeAll(w, prompts, func(p db.AiPrompt) AIPrompt { return aiPromptDTO(p, additions[p.ID]) })
}

// promptInput is the create and replace body of a prompt.
type promptInput struct {
	Name              string                      `json:"name"`
	Mode              string                      `json:"mode"`
	PromptText        string                      `json:"prompt_text"`
	SegmentContext    []string                    `json:"segment_context"`
	LanguageAdditions []aiprompt.LanguageAddition `json:"language_additions"`
}

// input turns the body into the service input; ok is false for an unknown mode.
func (in promptInput) input() (aiprompt.Input, bool) {
	out, ok := aiprompt.FlagsForMode(in.Mode)
	out.Name = strings.TrimSpace(in.Name)
	out.PromptText = in.PromptText
	out.SegmentContext = in.SegmentContext
	out.LanguageAdditions = in.LanguageAdditions
	return out, ok
}

func (h Handlers) decodePrompt(w http.ResponseWriter, r *http.Request) (aiprompt.Input, bool) {
	var req promptInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return aiprompt.Input{}, false
	}
	in, ok := req.input()
	if !ok {
		httputil.FieldError(w, http.StatusBadRequest, "mode", "mode must be qe, qe_ape, ape or mt")
		return aiprompt.Input{}, false
	}
	return in, true
}

func (h Handlers) createAIPrompt(w http.ResponseWriter, r *http.Request) {
	in, ok := h.decodePrompt(w, r)
	if !ok {
		return
	}
	p, additions, err := aiprompt.CreateTx(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), in)
	if err != nil {
		writeAIError(w, err, "create", "AI prompt")
		return
	}
	httputil.Created(w, aiPromptDTO(p, additions))
}

func (h Handlers) replaceAIPrompt(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "promptID")
	if err != nil {
		httputil.BadRequest(w, "invalid prompt ID")
		return
	}
	in, ok := h.decodePrompt(w, r)
	if !ok {
		return
	}
	p, additions, err := aiprompt.Edit(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), id, in)
	if err != nil {
		writeAIError(w, err, "update", "AI prompt")
		return
	}
	httputil.Data(w, aiPromptDTO(p, additions))
}

// --- Engines ---

func (h Handlers) listAIEngines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	engines, err := h.Queries.ListAIEngines(ctx, tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	rows, err := h.Queries.ListAIEngineRowsForTenant(ctx, tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	byEngine := map[int64][]db.AiEngineRow{}
	for _, row := range rows {
		byEngine[row.EngineID] = append(byEngine[row.EngineID], row)
	}
	writeAll(w, engines, func(e db.AiEngine) AIEngine { return aiEngineDTO(e, byEngine[e.ID]) })
}

// engineInput is the create and replace body of an engine.
type engineInput struct {
	Name            string         `json:"name"`
	Purpose         string         `json:"purpose"`
	Enabled         *bool          `json:"enabled"`
	TokenID         int64          `json:"token_id"`
	Model           string         `json:"model"`
	SystemPromptID  *int64         `json:"system_prompt_id"`
	BadOutputAction *string        `json:"bad_output_action"`
	Settings        map[string]any `json:"settings"`
}

func (in engineInput) row() aiengine.RowInput {
	return aiengine.RowInput{TokenID: in.TokenID, Model: in.Model, SystemPromptID: in.SystemPromptID, BadOutputAction: in.BadOutputAction, Settings: in.Settings}
}

func (h Handlers) createAIEngine(w http.ResponseWriter, r *http.Request) {
	var req engineInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	e, rows, err := aiengine.Create(r.Context(), h.Queries, h.Pool, h.AICatalog, auth.TenantIDFromContext(r.Context()),
		aiengine.Input{Name: req.Name, Purpose: req.Purpose, Row: req.row()})
	if err != nil {
		writeAIError(w, err, "create", "AI engine")
		return
	}
	httputil.Created(w, aiEngineDTO(e, rows))
}

// replaceAIEngine replaces the engine's name, state and provider row. The
// purpose is fixed at creation.
func (h Handlers) replaceAIEngine(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	var req engineInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	e, rows, err := aiengine.Update(r.Context(), h.Queries, h.Pool, h.AICatalog, auth.TenantIDFromContext(r.Context()), id,
		aiengine.UpdateInput{Name: req.Name, Enabled: req.Enabled, Row: req.row()})
	if err != nil {
		writeAIError(w, err, "update", "AI engine")
		return
	}
	httputil.Data(w, aiEngineDTO(e, rows))
}

func (h Handlers) deleteAIEngine(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	if err := aiengine.Delete(r.Context(), h.Queries, h.Pool, auth.TenantIDFromContext(r.Context()), id); err != nil {
		writeAIError(w, err, "delete", "AI engine")
		return
	}
	httputil.NoContent(w)
}
