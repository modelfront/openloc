package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"openloc/internal/aiusage"
	"openloc/internal/jsonutil"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// AI-as-MT: a minimal text-generation client that turns an AI
// provider (OpenAI / Anthropic) into a machine translator. The pipeline wires
// TranslateBatch behind an mt.Engine adapter (internal/mt/llm.go) so an AI
// engine of type 'mt' can be assigned exactly like a classic MT engine.
//
// Deliberately small: chat/messages completion with a system prompt that
// carries the engine's instruction text, llmBatchSize segments per request,
// bounded concurrency. No streaming, no function calling —
// translation is a single-shot request/response.

// Fallback models when an engine row names none — used by previews and the
// debug viewer, since a row cannot be saved without a model.
const (
	defaultOpenAIModel = "gpt-4o-mini"
	defaultClaudeModel = "claude-sonnet-5"
)

// llmConcurrency bounds parallel provider calls per run so a large file
// doesn't fan out hundreds of simultaneous requests (rate limits, cost
// spikes). It bounds concurrent *batches*, not segments.
const llmConcurrency = 5

// ProviderError is a provider-side failure (bad key, out of quota, rate
// limited, unreachable) from an AI translator call. It's carried up the stack
// so handlers can surface an actionable 4xx instead of an opaque 500 — the
// user needs to fix their key/billing, not file a bug. Kind classifies the
// failure for messaging; Detail is the provider's own reason when available;
// Code is the provider's machine-readable error code or type (for example
// "insufficient_quota"), empty when the body carries none.
type ProviderError struct {
	Provider string
	Status   int // 0 for network/transport failures
	Kind     string
	Detail   string
	Code     string
}

const (
	ProviderErrAuth        = "auth"        // key rejected (401/403)
	ProviderErrQuota       = "quota"       // out of credit (429 insufficient_quota)
	ProviderErrRateLimit   = "rate_limit"  // throttled (429)
	ProviderErrUnreachable = "unreachable" // transport failure
	ProviderErrProvider    = "provider"    // other non-2xx
)

var _ logging.LogSafe = (*ProviderError)(nil)

// LogMessage implements logging.LogSafe. A provider's message text can echo
// the request, so a log gets the classification and the provider's error code.
// An unreachable provider's Detail is the Go transport error, which carries no
// response body, so it stays.
func (e *ProviderError) LogMessage() string {
	msg := fmt.Sprintf("%s provider %s (status %d)", e.Provider, e.Kind, e.Status)
	switch {
	case e.Kind == ProviderErrUnreachable && e.Detail != "":
		return msg + ": " + e.Detail
	case e.Code != "":
		return msg + ": code " + e.Code
	}
	return msg
}

func (e *ProviderError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s provider %s (status %d): %s", e.Provider, e.Kind, e.Status, e.Detail)
	}
	return fmt.Sprintf("%s provider %s (status %d)", e.Provider, e.Kind, e.Status)
}

// TranslateConfig is everything needed to run one AI-as-MT batch.
type TranslateConfig struct {
	Provider   string // openai | claude
	Model      string // provider model id; falls back to the provider default when empty
	APIKey     string
	BaseURL    string // optional provider base URL override
	PromptText string // the engine prompt's instruction text (system prompt)
	SourceLang string
	TargetLang string
	// Settings are the provider config's tunable request parameters
	// (ai_engine_rows.settings). Nil = provider defaults.
	Settings Settings
	// SegmentContext is the optional per-segment context the prompt switched
	// on, in segmentprompt's key vocabulary. The contract describes exactly
	// these and no more.
	SegmentContext []string
	// Tagged marks the batch as carrying inline tag chips, which adds the tag
	// contract to the system prompt. Per-request, not per-engine: one file mixes
	// tagged and untagged segments.
	Tagged bool
}

// TranslateInput is one segment to translate. Content is the rendered segment
// prompt; empty falls back to the raw source Text, which is what
// engines without a segment prompt get.
type TranslateInput struct {
	ID   string
	Text string
	// Content is the rendered segment prompt as JSON.
	// Nil means the engine row has no segment prompt — the bare source text is
	// sent instead.
	Content json.RawMessage
}

// TranslateResult is one segment's MT output: the translation, and nothing
// else. An MT prompt's output mode declares no comments (MODE_FLAGS.mt).
type TranslateResult struct {
	Text string
}

// Translator runs AI-as-MT batches. Injected (rather than a package function)
// so the pipeline can swap a fake in tests without hitting a real provider.
type Translator interface {
	TranslateBatch(ctx context.Context, cfg TranslateConfig, rows []TranslateInput) ([]TranslateResult, error)
}

// HTTPTranslator is the production Translator — real provider calls.
type HTTPTranslator struct {
	client *http.Client
	rec    llmdebug.Recorder // nil = capture off (production)
	usage  aiusage.Recorder  // nil = accounting off (tests, chat previews)
}

// NewHTTPTranslator returns the production translator (60s timeout per call —
// generous headroom for long segments on slower models).
func NewHTTPTranslator() *HTTPTranslator {
	return &HTTPTranslator{client: netguard.Client(60 * time.Second)}
}

// WithUsageRecorder turns on token accounting. Separate from WithRecorder: the
// debug ring is a dev tool, this is a ledger, and they are switched on in
// different environments.
func (t *HTTPTranslator) WithUsageRecorder(r aiusage.Recorder) *HTTPTranslator {
	t.usage = r
	return t
}

// recordUsage is a no-op when accounting is off, so the call sites stay flat.
func (t *HTTPTranslator) recordUsage(ctx context.Context, provider, model string, u aiusage.Usage) {
	if t.usage != nil {
		t.usage.Record(ctx, provider, model, u)
	}
}

// WithRecorder enables the dev-only request/response capture (see
// internal/llmdebug). Returns t for chaining at the wiring sites.
func (t *HTTPTranslator) WithRecorder(r llmdebug.Recorder) *HTTPTranslator {
	t.rec = r
	return t
}

// TranslateBatch translates rows in order, preserving index alignment (result
// i is the translation of rows i). Blank inputs pass through as empty and are
// never sent. The first provider error aborts everything.
//
// Rows are chunked into llmBatchSize-sized provider requests and
// matched back by id. A segment the model omitted is retried once on its own;
// if it's still missing the result stays empty, and the pipeline skips the row
// rather than writing a blank translation over it.
func (t *HTTPTranslator) TranslateBatch(ctx context.Context, cfg TranslateConfig, rows []TranslateInput) ([]TranslateResult, error) {
	if !CanMachineTranslate(cfg.Provider) {
		return nil, fmt.Errorf("provider %q cannot be used as an AI translator", cfg.Provider)
	}
	out := make([]TranslateResult, len(rows))
	pos := make(map[string]int, len(rows))
	items := make([]batchItem, 0, len(rows))
	for i, r := range rows {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		items = append(items, batchItem{ID: r.ID, Content: translateContent(r)})
		pos[r.ID] = i
	}
	if len(items) == 0 {
		return out, nil
	}

	system := systemPrompt(cfg)
	cc := callConfig{
		provider: cfg.Provider, model: cfg.Model, apiKey: cfg.APIKey,
		baseURL: cfg.BaseURL, kind: llmdebug.KindMT, mode: ModeMT, settings: cfg.Settings,
	}
	apply := func(item batchItem, raw json.RawMessage) {
		out[pos[item.ID]] = parseTargetOutput(string(raw))
	}

	missing, err := t.runBatches(ctx, cc, system, chunkBatches(items, batchItemContent), apply)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		retry := make([][]batchItem, 0, len(missing))
		for _, m := range missing {
			retry = append(retry, []batchItem{m.item})
		}
		// Still-missing segments keep their zero TranslateResult — an empty
		// Text tells saveMTChunk to leave the row untranslated instead of
		// marking it done with blank content.
		rcc := cc
		rcc.retry = true
		if _, err := t.runBatches(ctx, rcc, system, retry, apply); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// translateContent is the per-segment user body: the rendered segment prompt
// when the engine row has one, else the bare source text.
func translateContent(r TranslateInput) json.RawMessage {
	if len(r.Content) > 0 {
		return r.Content
	}
	// No segment prompt: the source text under its own key, so every segment is
	// the same flat object whether or not a prompt chose extra context. A bare
	// string would have no name to flatten under and would be dropped.
	out, err := jsonutil.MarshalNoEscape(map[string]string{"source": r.Text})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

// parseTargetOutput extracts the translation from one per-segment object.
// Expected shape is {"target": string}; tolerates fences/preamble via
// extractJSONObject and falls back to the raw text when the contract wasn't
// followed — a chatty model should still translate rather than fail the batch.
func parseTargetOutput(raw string) TranslateResult {
	if payload := extractJSONObject(raw); payload != "" {
		var p struct {
			Target *string `json:"target"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Target != nil {
			return TranslateResult{Text: strings.TrimSpace(*p.Target)}
		}
	}
	return TranslateResult{Text: strings.TrimSpace(raw)}
}

// callConfig is the provider-agnostic transport config for one completion
// call — shared by translate (AI-as-MT) and predict (LLM QE+APE).
// kind labels the call for the dev debug recorder (llmdebug.Kind*).
// maxTokens scales Anthropic's required output cap with the batch
// size; 0 uses the single-response default.
type callConfig struct {
	provider, model, apiKey, baseURL, kind string
	// mode is the output mode the reply must satisfy (ModeMT / ModeQE /
	// ModeAPE). It selects the JSON schema sent to the provider. Carried
	// separately from kind: kind labels the call for the debug recorder, and
	// what goes on the wire must not depend on a logging concern.
	mode      string
	maxTokens int
	settings  Settings

	// How the request came to be, for the debug feed only — never on the wire.
	// A reader looking at a 1-segment request next to a 10-segment one needs to
	// know whether it is the tail of a file, a segment too long to travel with
	// others, or a retry of one the model skipped. segments answers the first
	// two, retry the third.
	segments int
	retry    bool
}

// outputSchema is the JSON schema for this call's replies, or nil when the
// model can't take one (the prose contract carries it alone then).
func (cc callConfig) outputSchema(resolvedModel string) map[string]any {
	if !SupportsStructuredOutput(cc.provider, resolvedModel) {
		return nil
	}
	return ResultsSchema(cc.mode)
}

// resolveModel is the model a request will actually name: the configured one,
// or the provider's default when it is blank.
//
// Everything that must agree about "which model" goes through this — the
// outbound body, and the system prompt's decision about whether the provider
// will enforce a schema. Two spellings of the default could leave a prompt
// omitting the format instructions for a request that carries no schema.
func resolveModel(provider, model string) string {
	return orDefault(model, defaultModelFor(provider))
}

// complete issues one system+user chat completion against the configured
// provider and returns the model's text output.
func (t *HTTPTranslator) complete(ctx context.Context, cc callConfig, system, user string) (string, error) {
	switch cc.provider {
	case ProviderOpenAI:
		return t.openAI(ctx, cc, system, user)
	case ProviderClaude:
		return t.claude(ctx, cc, system, user)
	default:
		return "", fmt.Errorf("unknown provider %q", cc.provider)
	}
}

// systemPrompt combines the engine's instruction text with the batched MT
// output contract. The prompt
// editor previews the same contract by asking the server to assemble it
// (internal/aiprompt preview), so there is no second copy.
func systemPrompt(cfg TranslateConfig) string {
	return translateInstruction(cfg) + translateSuffix(cfg)
}

// SchemaEnforced reports whether the provider will constrain this request's
// reply to ResultsSchema, which decides how much of the contract the prompt
// still has to spell out.
//
// An unknown model is assumed to take one. That case is the prompt editor,
// which previews a prompt no engine is attached to yet: assuming otherwise
// prints the legacy prose contract beside the schema that replaced it, and
// every current model would be shown a block it never receives. Every runtime
// caller has a model — an LLM engine row cannot be saved without one — so this
// only ever decides what the preview says.
func SchemaEnforced(provider, model string) bool {
	if strings.TrimSpace(model) == "" && strings.TrimSpace(provider) == "" {
		return true
	}
	return SupportsStructuredOutput(provider, resolveModel(provider, model))
}

// translateInstruction is the tenant's own text with the language tokens
// resolved, or the built-in stand-in when an engine carries none.
func translateInstruction(cfg TranslateConfig) string {
	instruction := strings.TrimSpace(cfg.PromptText)
	if instruction == "" {
		instruction = "You are a professional translator."
	}
	return substituteLanguages(instruction, cfg.SourceLang, cfg.TargetLang)
}

// translateSuffix is everything the platform appends to the instruction: the
// batched output contract. The language pair is named by the instruction
// itself, through the <source_lang> / <target_lang> tokens. Split out so the prompt
// preview (internal/aiprompt) can show the appended half on its own — the UI
// marks it read-only, since a tenant cannot change it.
func translateSuffix(cfg TranslateConfig) string {
	formats := translateFormats(cfg)
	return formats.OutputFormat
}

func translateFormats(cfg TranslateConfig) promptFormats {
	return batchFormats(
		`"target": string`,
		SchemaEnforced(cfg.Provider, cfg.Model))
}

func (t *HTTPTranslator) openAI(ctx context.Context, cc callConfig, system, user string) (string, error) {
	base := orDefault(cc.baseURL, "https://api.openai.com")
	model := resolveModel(ProviderOpenAI, cc.model)
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	// Sets temperature: 0 by default, then overlays the provider config's
	// settings.
	cc.settings.applyOpenAI(payload, model)
	if schema := cc.outputSchema(model); schema != nil {
		payload["response_format"] = openAISchemaFormat(schema)
	}
	body, err := jsonutil.MarshalNoEscape(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	SetOpenAIAuth(req, cc.apiKey, base)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	raw, status, err := t.do(req, cc.provider)
	t.record(ctx, cc, req.URL.String(), body, raw, status, err, time.Since(start), system, user)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode openai response: %w", err)
	}
	// Recorded before the no-choices check below: the tokens were spent whether
	// or not the reply was usable.
	t.recordUsage(ctx, cc.provider, model, aiusage.Usage{
		InputTokens: parsed.Usage.PromptTokens, OutputTokens: parsed.Usage.CompletionTokens,
	})
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func (t *HTTPTranslator) claude(ctx context.Context, cc callConfig, system, user string) (string, error) {
	base := orDefault(cc.baseURL, "https://api.anthropic.com")
	model := resolveModel(ProviderClaude, cc.model)
	maxTokens := cc.maxTokens
	if maxTokens <= 0 {
		maxTokens = batchMaxTokens(1, cc.settings.thinkingEnabled(cc.provider, model))
	}
	payload := map[string]any{
		"model":  model,
		"system": system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	// Sets max_tokens (batch-derived unless the config pins one) plus thinking,
	// effort and any sampling knobs the model accepts.
	cc.settings.applyClaude(payload, model, maxTokens)
	if schema := cc.outputSchema(model); schema != nil {
		setClaudeOutputFormat(payload, schema)
	}
	body, err := jsonutil.MarshalNoEscape(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", cc.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	raw, status, err := t.do(req, cc.provider)
	t.record(ctx, cc, req.URL.String(), body, raw, status, err, time.Since(start), system, user)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode claude response: %w", err)
	}
	t.recordUsage(ctx, cc.provider, model, aiusage.Usage{
		InputTokens: parsed.Usage.InputTokens, OutputTokens: parsed.Usage.OutputTokens,
	})
	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// do executes req and returns the body and status, mapping transport failures
// and non-2xx responses to a *ProviderError classified by kind so callers can
// turn it into an actionable, client-safe message. The raw body is returned
// even on error so the debug recorder can show what the provider actually said
// (status 0 = transport failure, no response).
func (t *HTTPTranslator) do(req *http.Request, provider string) ([]byte, int, error) {
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, &ProviderError{Provider: provider, Kind: ProviderErrUnreachable, Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, resp.StatusCode, nil
	}
	msg, code := parseProviderError(raw)
	pe := &ProviderError{Provider: provider, Status: resp.StatusCode, Detail: msg, Code: code}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		pe.Kind = ProviderErrAuth
	case code == "insufficient_quota":
		pe.Kind = ProviderErrQuota
	case resp.StatusCode == http.StatusTooManyRequests:
		pe.Kind = ProviderErrRateLimit
	default:
		pe.Kind = ProviderErrProvider
	}
	return raw, resp.StatusCode, pe
}

// parseProviderError extracts the human-readable message and error code from an
// OpenAI/Anthropic error body ({"error":{"message","code","type"}}), falling
// back to the raw (truncated) body when it isn't the expected shape.
func parseProviderError(raw []byte) (message, code string) {
	var p struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &p) == nil && p.Error.Message != "" {
		code = p.Error.Code
		if code == "" {
			code = p.Error.Type
		}
		return truncate(strings.TrimSpace(p.Error.Message), 300), truncate(code, 64)
	}
	return truncate(strings.TrimSpace(string(raw)), 300), ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
