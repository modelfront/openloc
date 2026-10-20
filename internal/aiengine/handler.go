// Package aiengine is the HTTP handler for AI engines. An
// engine is a name, a purpose and one wildcard row carrying its token, model,
// settings and (for LLMs) system prompt + bad-output action. Tenant-scoped;
// manager-read, owner/admin-write (wired in api.go).
// The row drives execution: AI-as-MT (pipeline.resolveAIMTEngine) and QE+APE
// workflow steps (pipeline.RunAIStep, which reaches the engine through the
// step's AI router) run it for whatever language pair the job supplies.
package aiengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/aiprompt"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/enginehealth"
	"openloc/internal/extension"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// Engine purposes: an engine is used EITHER as an MT engine
// (settings MT assignment) OR as a QE+APE workflow-step engine — never both.
const (
	PurposeMT    = "mt"
	PurposeQEAPE = "qe_ape"
)

// CheckQEAPEEngine verifies that engineID is one of this tenant's engines and
// carries the qe_ape purpose (an MT engine can't review). It is the
// per-engine check behind an AI router (airouter.Validate); a step never
// names an engine directly. Returns a client-safe message ("" = valid).
func CheckQEAPEEngine(ctx context.Context, q db.Querier, tenantID, engineID int64) string {
	engine, err := q.GetAIEngine(ctx, db.GetAIEngineParams{ID: engineID, TenantID: tenantID})
	if err != nil {
		return fmt.Sprintf("AI engine %d not found", engineID)
	}
	if engine.Purpose != PurposeQEAPE {
		return fmt.Sprintf("AI engine %q is an MT engine — a workflow step needs a QE+APE engine", engine.Name)
	}
	return ""
}

// modeLabel is the display spelling of a prompt mode, for messages the user
// reads. Spelled out per mode rather than derived from the slug: `qe_ape` reads
// "QE+APE", which no mechanical transform of the underscore produces, and a
// derived label is invisible to a search for the label itself.
// Mirrors MODE_LABELS in web/src/components/ai/output-mode.ts.
func modeLabel(mode string) string {
	switch mode {
	case "qe_ape":
		return "QE+APE"
	case "qe":
		return "QE"
	case "ape":
		return "APE"
	default:
		return "MT"
	}
}

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
	// catalog gates which models a tenant may pin on a row.
	catalog *ai.Catalog
}

func NewHandler(q db.Querier, pool *pgxpool.Pool, catalog *ai.Catalog) *Handler {
	return &Handler{queries: q, pool: pool, catalog: catalog}
}

var validBadOutput = map[string]bool{"reject": true, "comment": true, "ignore": true}

// engineInput is the write shape: a name, a purpose and the full set of rows
// (replace-all). Purpose is set at creation and immutable afterwards.
type engineInput struct {
	Name    string     `json:"name"`
	Purpose string     `json:"purpose"`
	Rows    []rowInput `json:"rows"`
	// Absent keeps the current state, so a save that only edits rows cannot
	// silently switch an engine back on.
	Enabled *bool `json:"enabled"`
}

type rowInput struct {
	SourceLang  string   `json:"source_lang"`
	TargetLangs []string `json:"target_langs"`
	// SystemPromptID nil ⇒ an extension row (no LLM prompt, no
	// bad-output action). Otherwise both are required.
	SystemPromptID  *int64  `json:"system_prompt_id"`
	BadOutputAction *string `json:"bad_output_action"`
	// The row's own vendor connection: the token, model and
	// model settings it runs with.
	TokenID  *int64         `json:"token_id"`
	Model    string         `json:"model"`
	Settings map[string]any `json:"settings"`
}

// engineRow is the read shape for a row.
type engineRow struct {
	ID              int64           `json:"id"`
	SourceLang      string          `json:"source_lang"`
	TargetLangs     []string        `json:"target_langs"`
	SystemPromptID  *int64          `json:"system_prompt_id"`
	BadOutputAction *string         `json:"bad_output_action"`
	TokenID         *int64          `json:"token_id"`
	Model           string          `json:"model"`
	Settings        json.RawMessage `json:"settings"`
}

// engineResponse is an engine with its rows nested. `enabled` rides along on
// the embedded row.
type engineResponse struct {
	db.AiEngine
	Rows []engineRow `json:"rows"`
	// Why this engine cannot translate, or "" when it can.
	// Same field, same values and same meaning as an MT engine's, so the router
	// surfaces can flag either kind without caring which it is.
	Unusable string `json:"unusable"`
}

// health reports whether an AI engine can translate.
//
// A row's token is required at save (`validateRow`), so a null one means the
// key was DELETED from AI settings — the engine kept its prompts, model and
// routes, and this is what says it can no longer run.
func health(engine db.AiEngine, rows []engineRow) enginehealth.Reason {
	hasKey := len(rows) > 0
	for _, r := range rows {
		if r.TokenID == nil {
			hasKey = false
			break
		}
	}
	return enginehealth.Of(engine.Enabled, true, hasKey)
}

// withHealth stamps the verdict onto an assembled response.
func withHealth(ctx context.Context, e engineResponse) engineResponse {
	e.Unusable = string(enginehealth.Shown(health(e.AiEngine, e.Rows), auth.IsDemo(ctx)))
	return e
}

func toEngineRow(r db.AiEngineRow) engineRow {
	// Settings is jsonb NOT NULL DEFAULT '{}', but a row written before the
	// column existed can still surface as an empty slice — serialize that as an
	// object so the client never has to handle `null` here.
	settings := json.RawMessage(r.Settings)
	if len(settings) == 0 {
		settings = json.RawMessage("{}")
	}
	return engineRow{
		ID:              r.ID,
		SourceLang:      r.SourceLang,
		TargetLangs:     r.TargetLangs,
		SystemPromptID:  r.SystemPromptID,
		BadOutputAction: r.BadOutputAction,
		TokenID:         r.TokenID,
		Model:           r.Model,
		Settings:        settings,
	}
}

// validate normalizes the input and checks every row against the engine's
// purpose. Returns a client-safe message (empty = valid). Also
// verifies referenced prompts belong to the tenant (cross-tenant guard).
func (h *Handler) validate(ctx context.Context, tenantID int64, in *engineInput, purpose string) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "name is required"
	}
	// An AI engine is exactly one row = a provider (+ prompt for LLMs).
	// No language routing — the row is a catch-all (langcode.Wildcard), so it
	// applies to whatever language pair the workflow step / MT assignment supplies.
	if len(in.Rows) != 1 {
		return "an AI engine needs exactly one provider"
	}
	row := &in.Rows[0]
	row.SourceLang = langcode.Wildcard
	row.TargetLangs = []string{langcode.Wildcard}
	if row.TokenID == nil {
		return "select a model"
	}
	// The token's vendor decides extension vs LLM, and gates which
	// models + settings are legal.
	vendor, msg := ValidateModelSelection(ctx, h.queries, h.catalog, tenantID, *row.TokenID, row.Model, row.Settings)
	// Checked ahead of `msg`: an MT vendor has no registered models, so the
	// generic "invalid model for provider deepl" would win and name the wrong
	// problem. `vendor` is empty only when the token lookup failed, and that is
	// not an MT provider, so this cannot mask a genuine token error.
	//
	// DeepL and Google are translation endpoints, not models an AI engine can
	// drive: they can't follow the QE+APE output contract and they take no
	// prompt. Their key is spent by an MT engine instead,
	// which is what the translate pipeline reads — so they
	// belong to NEITHER purpose, not just to the QE+APE one.
	if ai.IsMTEngineProvider(vendor) {
		return fmt.Sprintf("%s is a translation vendor, not an AI model — it is configured as an MT engine, not an AI engine", vendor)
	}
	if msg != "" {
		return msg
	}
	row.Model = strings.TrimSpace(row.Model)

	if _, isExt := extension.IDFromProvider(vendor); isExt {
		// An extension reviews with its own predict — no system/segment prompt
		// or bad-output action. It can't machine-translate, so an MT engine
		// can't select it.
		if purpose == PurposeMT {
			return "a custom provider can't machine-translate — pick an LLM model for an MT engine"
		}
		row.SystemPromptID = nil
		row.BadOutputAction = nil
		return ""
	}

	// LLM provider — a mode-compatible system prompt and a bad-output action
	// are both required. The per-segment context lives on the prompt.
	if row.SystemPromptID == nil {
		return "select a system prompt"
	}
	prompt, err := h.queries.GetAIPrompt(ctx, db.GetAIPromptParams{ID: *row.SystemPromptID, TenantID: tenantID})
	if err != nil {
		return fmt.Sprintf("system prompt %d not found", *row.SystemPromptID)
	}
	// Prompt-mode ↔ purpose compatibility.
	mode := aiprompt.Mode(prompt)
	if purpose == PurposeMT && mode != "mt" {
		return fmt.Sprintf("prompt %q is a %s prompt — an MT engine needs an MT prompt", prompt.Name, modeLabel(mode))
	}
	if purpose == PurposeQEAPE && mode == "mt" {
		return fmt.Sprintf("prompt %q is an MT prompt — a QE+APE engine needs a QE, APE or QE+APE prompt", prompt.Name)
	}
	if row.BadOutputAction == nil || strings.TrimSpace(*row.BadOutputAction) == "" {
		return "select a bad-output action"
	}
	a := strings.TrimSpace(*row.BadOutputAction)
	if !validBadOutput[a] {
		return "bad_output_action must be one of: reject, comment, ignore"
	}
	row.BadOutputAction = &a
	return ""
}

// insertRows writes the rows for an engine in order, returning the created rows.
func insertRows(ctx context.Context, qtx *db.Queries, engineID int64, rows []rowInput) ([]db.AiEngineRow, error) {
	out := make([]db.AiEngineRow, 0, len(rows))
	for i, row := range rows {
		settings, err := SettingsBytes(row.Settings)
		if err != nil {
			return nil, err
		}
		created, err := qtx.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
			EngineID:        engineID,
			SourceLang:      row.SourceLang,
			TargetLangs:     row.TargetLangs,
			SystemPromptID:  row.SystemPromptID,
			BadOutputAction: row.BadOutputAction,
			SortOrder:       int32(i),
			TokenID:         row.TokenID,
			Model:           row.Model,
			Settings:        settings,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, created)
	}
	return out, nil
}

func response(ctx context.Context, engine db.AiEngine, rows []db.AiEngineRow) engineResponse {
	out := engineResponse{AiEngine: engine, Rows: make([]engineRow, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, toEngineRow(r))
	}
	return withHealth(ctx, out)
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	engines, err := h.queries.ListAIEngines(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engines")
		return
	}
	rows, err := h.queries.ListAIEngineRowsForTenant(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "AI engine rows")
		return
	}
	byEngine := make(map[int64][]engineRow, len(engines))
	for _, row := range rows {
		byEngine[row.EngineID] = append(byEngine[row.EngineID], toEngineRow(row))
	}
	out := make([]engineResponse, 0, len(engines))
	for _, e := range engines {
		er := withHealth(r.Context(), engineResponse{AiEngine: e, Rows: byEngine[e.ID]})
		if er.Rows == nil {
			er.Rows = []engineRow{}
		}
		out = append(out, er)
	}
	httputil.Data(w, out)
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	var in engineInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	// Purpose is chosen at creation; omitted input defaults to qe_ape.
	in.Purpose = strings.TrimSpace(in.Purpose)
	if in.Purpose == "" {
		in.Purpose = PurposeQEAPE
	}
	if in.Purpose != PurposeMT && in.Purpose != PurposeQEAPE {
		httputil.BadRequest(w, "purpose must be one of: mt, qe_ape")
		return
	}
	if msg := h.validate(r.Context(), tenantID, &in, in.Purpose); msg != "" {
		httputil.BadRequest(w, msg)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "create", "AI engine")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := db.New(tx)

	engine, err := qtx.CreateAIEngine(r.Context(), db.CreateAIEngineParams{TenantID: tenantID, Name: in.Name, Purpose: in.Purpose})
	if err != nil {
		httputil.ServerError(w, "create", "AI engine")
		return
	}
	rows, err := insertRows(r.Context(), qtx, engine.ID, in.Rows)
	if err != nil {
		httputil.ServerError(w, "create", "AI engine rows")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "create", "AI engine")
		return
	}
	httputil.Created(w, response(r.Context(), engine, rows))
}

func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	var in engineInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	engine, rows, err := update(r.Context(), h.queries, h.pool, h.catalog, tenantID, id, in)
	if err != nil {
		if msg, ok := AsValidation(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServiceError(w, err, "update", "AI engine")
		return
	}
	httputil.Data(w, response(r.Context(), engine, rows))
}

// HandleDelete removes an engine; 409 while any live router still references
// it (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "engineID")
	if err != nil {
		httputil.BadRequest(w, "invalid engine ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, h.pool, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "AI engine")
		return
	}
	httputil.NoContent(w)
}
