package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"openloc/internal/errtrack"
	"openloc/internal/jsonutil"
	"openloc/internal/llmdebug"
)

// LLM-backed QE+APE: run quality
// estimation / automatic post-editing against a text-generation provider
// (OpenAI / Anthropic). The model is instructed to
// return our expected format per segment, one shape per output mode —
//
//	QE:     {"approved": bool, "score": 0-100, "comment": string|null}
//	APE:    {"postedit": string|null, "score": 0-100, "comment": string|null}
//	QE+APE: {"postedit": string|null, "approved": bool, "score": 0-100,
//	         "postedit_approved": bool|null, "postedit_score": 0-100|null,
//	         "comment": string|null}
//
// — which the pipeline routes through the same 4-outcome logic as an
// extension's predict result.
//
// The split follows each mode's own output flags. QE judges and must not
// rewrite. APE rewrites and must not judge: it stores out_status = false, so it
// is asked for no verdict, never auto-approves, and every segment it touches
// lands in needs_review with the postedit applied. QE+APE does both —
// approved/score judge the ORIGINAL translation, postedit_approved/
// postedit_score judge the postedit, and the postedit is applied only when
// postedit_approved (an unapproved one is discarded).
//
// The prompt editor previews this contract by asking the server to assemble it
// (internal/aiprompt preview), so there is no second copy to keep in sync.
//
// Those per-segment objects travel batched — llmBatchSize
// segments per request, wrapped in {"results":[…]} and matched back by id.

// PredictConfig is everything needed to run one LLM QE+APE batch.
type PredictConfig struct {
	Provider   string // openai | claude
	Model      string // provider model id; falls back to the provider default when empty
	APIKey     string
	BaseURL    string // optional provider base URL override
	PromptText string // the engine row's system-prompt instruction text
	SourceLang string
	TargetLang string
	// Settings are the provider config's tunable request parameters
	// (ai_engine_rows.settings). Nil = provider defaults.
	Settings Settings
	// SegmentContext is the optional per-segment context the prompt switched
	// on, in segmentprompt's key vocabulary. The contract describes exactly
	// these and no more — a field the segment will not carry must not be named.
	SegmentContext []string
	// Mode is the prompt's output mode — ModeQE, ModeAPE or ModeQEAPE — and
	// picks the contract. Empty means QE+APE, the widest of the three.
	//
	// Not a pair of booleans: the three modes differ in which HALF they ask
	// for. QE judges and does not rewrite, APE rewrites and does not judge —
	// it stores out_status = false and must never be asked for a verdict —
	// and QE+APE does both.
	Mode string
	// Tagged marks the batch as carrying inline tag chips, which adds the tag
	// contract to the system prompt. Per-request, not per-engine: one file mixes
	// tagged and untagged segments.
	Tagged bool
}

// mode resolves the config's output mode, defaulting an unset or unknown one
// to QE+APE — the widest contract, and the one an LLM engine row with a
// reviewing prompt has always run (see aiprompt.Mode).
func (c PredictConfig) mode() string {
	switch c.Mode {
	case ModeQE, ModeAPE:
		return c.Mode
	}
	return ModeQEAPE
}

// wantsPostedit reports whether the model may return a corrected translation.
func (c PredictConfig) wantsPostedit() bool { return c.mode() != ModeQE }

// PredictInput is one segment to evaluate.
type PredictInput struct {
	ID          string
	Source      string
	Translation string
	// Content is the rendered segment prompt as JSON — the tenant-authored per-segment body. Nil falls back
	// to the built-in {"source","translation"} object, which is what rows
	// without a segment prompt get.
	Content json.RawMessage
}

// PredictResult is the model's verdict for one segment, in our expected
// format. Invalid=true means the model's output didn't match the contract —
// the caller applies the engine row's bad-output action; Raw carries the
// offending output for logging.
type PredictResult struct {
	ID       string
	Approved bool     // verdict on the ORIGINAL translation
	Score    *float64 // 0-100 quality of the ORIGINAL; nil when omitted
	Comment  *string  // reviewer-facing remark; nil when the model had nothing to flag
	Postedit *string  // corrected translation (APE); nil when none
	// Verdict + 0-100 quality of the postedit itself (APE). nil when there is
	// no postedit (or the model omitted them — treated as not approved).
	PosteditApproved *bool
	PosteditScore    *float64
	Invalid          bool
	Raw              string
}

// Predictor runs LLM QE+APE batches. Injected (like Translator) so the
// pipeline can swap a fake in tests without hitting a real provider.
type Predictor interface {
	PredictBatch(ctx context.Context, cfg PredictConfig, rows []PredictInput) ([]PredictResult, error)
}

// PredictBatch evaluates rows in order, preserving index alignment (result i
// is the verdict on rows i, carrying the same ID).
//
// Rows are chunked into llmBatchSize-sized provider requests instead
// of one request per segment, and results are matched back by id (not
// position) so a model that reorders its array can't misattribute a verdict.
// Segments the model omitted are retried once as single-segment requests
// before being marked Invalid — one chatty reply must not reject nine good
// segments. The first provider error still aborts everything.
func (t *HTTPTranslator) PredictBatch(ctx context.Context, cfg PredictConfig, rows []PredictInput) ([]PredictResult, error) {
	if !CanPredict(cfg.Provider) {
		return nil, fmt.Errorf("provider %q cannot run LLM QE+APE", cfg.Provider)
	}
	out := make([]PredictResult, len(rows))
	pos := make(map[string]int, len(rows))
	items := make([]batchItem, len(rows))
	for i, r := range rows {
		items[i] = batchItem{ID: r.ID, Content: predictContent(r)}
		pos[r.ID] = i
		out[i].ID = r.ID
	}

	system := predictSystemPrompt(cfg)
	kind := llmdebug.KindQE
	if cfg.wantsPostedit() {
		// One debug label for both post-editing modes: the capture is about
		// what kind of work the call did, not which fields it asked for.
		kind = llmdebug.KindAPE
	}
	cc := callConfig{
		provider: cfg.Provider, model: cfg.Model, apiKey: cfg.APIKey,
		baseURL: cfg.BaseURL, kind: kind, mode: cfg.mode(), settings: cfg.Settings,
	}

	apply := func(item batchItem, raw json.RawMessage) {
		i := pos[item.ID]
		res := parsePredictOutput(string(raw), cfg.mode())
		res.ID = item.ID
		out[i] = res
	}

	missing, err := t.runBatches(ctx, cc, system, chunkBatches(items, batchItemContent), apply)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		// Split-and-retry: one request per still-unanswered segment.
		retry := make([][]batchItem, 0, len(missing))
		for _, m := range missing {
			retry = append(retry, []batchItem{m.item})
		}
		rcc := cc
		rcc.retry = true
		stillMissing, err := t.runBatches(ctx, rcc, system, retry, apply)
		if err != nil {
			return nil, err
		}
		for _, m := range stillMissing {
			out[pos[m.item.ID]] = PredictResult{ID: m.item.ID, Invalid: true, Raw: m.raw}
		}
	}
	return out, nil
}

// predictContent is the per-segment user body: the rendered segment prompt
// when the engine row has one, else the built-in JSON pairing so source and
// translation can't be conflated.
func predictContent(r PredictInput) json.RawMessage {
	if len(r.Content) > 0 {
		return r.Content
	}
	body, err := jsonutil.MarshalNoEscape(map[string]string{"source": r.Source, "translation": r.Translation})
	if err != nil {
		// map[string]string can't fail to marshal; fall back rather than lose
		// the segment if that ever changes.
		return json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("source: %s\ntranslation: %s", r.Source, r.Translation)))
	}
	return body
}

func batchItemContent(b batchItem) string { return string(b.Content) }

// batchMiss is a segment the model didn't answer for, with the raw reply that
// omitted it (kept for the bad-output action's log/comment).
type batchMiss struct {
	item batchItem
	raw  string
}

// completeInKeySlot runs one provider request inside a slot of the key's
// process-wide limit; the slot is returned even if the request panics.
func (t *HTTPTranslator) completeInKeySlot(ctx context.Context, cc, bcc callConfig, system, user string) (string, error) {
	release, err := keySlots.acquire(ctx, cc)
	if err != nil {
		return "", err
	}
	defer release()
	return t.complete(ctx, bcc, system, user)
}

// runBatches issues one provider request per batch, llmConcurrency at a time
// and within the key's process-wide limit (keySlots), and hands each matched
// per-segment object to apply. Returns the segments no batch answered for.
// The first provider error aborts (nothing partial is reported as success).
func (t *HTTPTranslator) runBatches(
	ctx context.Context, cc callConfig, system string,
	batches [][]batchItem, apply func(batchItem, json.RawMessage),
) ([]batchMiss, error) {
	misses := make([][]batchMiss, len(batches))
	errs := make([]error, len(batches))

	sem := make(chan struct{}, llmConcurrency)
	var wg sync.WaitGroup
	for bi, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(bi int, batch []batchItem) {
			// See spellcheck.CheckBatch: a panic in this goroutine cannot be
			// recovered by the request that spawned it, so without this it takes
			// the process down unreported.
			defer errtrack.Monitor(ctx, "ai", cc.kind+"-batch")
			defer wg.Done()
			defer func() { <-sem }()

			user, err := batchUserMessage(batch)
			if err != nil {
				errs[bi] = err
				return
			}
			bcc := cc
			bcc.segments = len(batch)
			bcc.maxTokens = batchMaxTokens(len(batch), cc.settings.thinkingEnabled(cc.provider, cc.model))
			raw, err := t.completeInKeySlot(ctx, cc, bcc, system, user)
			if err != nil {
				errs[bi] = err
				return
			}
			found, envelope := parseBatchResults(raw)
			// A one-segment batch (which is what the split-retry sends) has an
			// unambiguous answer even when the model skips the envelope and
			// replies with a bare object — or, for MT, bare text. Treating that
			// as the segment's result is what makes the retry pass reliable.
			// Only when the model ignored the format entirely, though: an empty
			// results array is a deliberate "no answer for this one".
			if len(batch) == 1 && len(found) == 0 && !envelope {
				apply(batch[0], json.RawMessage(raw))
				return
			}
			for _, item := range batch {
				if obj, ok := found[item.ID]; ok {
					apply(item, obj)
					continue
				}
				misses[bi] = append(misses[bi], batchMiss{item: item, raw: raw})
			}
		}(bi, batch)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	var all []batchMiss
	for _, m := range misses {
		all = append(all, m...)
	}
	return all, nil
}

// predictSystemPrompt combines the engine row's instruction text with the
// batched output contract.
func predictSystemPrompt(cfg PredictConfig) string {
	return predictInstruction(cfg) + predictSuffix(cfg)
}

// predictInstruction is the tenant's own text with the language tokens
// resolved, or the built-in stand-in when a row carries none.
func predictInstruction(cfg PredictConfig) string {
	instruction := strings.TrimSpace(cfg.PromptText)
	if instruction == "" {
		instruction = "You are an expert translation quality evaluator."
	}
	return substituteLanguages(instruction, cfg.SourceLang, cfg.TargetLang)
}

// predictSuffix is everything the platform appends to the instruction: the
// batched output contract. Split out from
// predictSystemPrompt so the prompt preview (internal/aiprompt) can show the
// appended half on its own — the UI marks it read-only, since a tenant cannot
// change it.
func predictSuffix(cfg PredictConfig) string {
	formats := predictFormats(cfg)
	return formats.OutputFormat
}

func predictFormats(cfg PredictConfig) promptFormats {

	var fields string
	switch cfg.mode() {
	case ModeQE:
		fields = `"approved": true or false, "score": number, "comment": string or null`
	case ModeAPE:
		// No verdict and no score: an APE prompt declares no status output, so
		// it never decides whether a segment ships, and scoring is that same
		// judgement by another name.
		fields = `"postedit": string or null, "comment": string or null`
	default:
		fields = `"postedit": string or null, "approved": true or false, "score": number, "postedit_approved": true or false or null, "postedit_score": number or null, "comment": string or null`
	}
	return batchFormats(
		fields, SchemaEnforced(cfg.Provider, cfg.Model))
}

// parsePredictOutput maps one raw per-segment object to a PredictResult for
// the given mode. Tolerates chatty wrappers (code fences, preamble) by slicing
// the first '{' to the last '}'.
//
// Each mode is validated on the one field it cannot answer without, which is
// how a reply that ignored the contract is told from one that simply had
// nothing to report: "approved" for the judging modes, "postedit" for APE.
// Both may be null for APE — an explicit null is an answer ("nothing to fix"),
// an absent key is not.
func parsePredictOutput(raw, mode string) PredictResult {
	res := PredictResult{Raw: raw}
	payload := extractJSONObject(raw)
	if payload == "" {
		res.Invalid = true
		return res
	}
	var p struct {
		Approved         *bool           `json:"approved"`
		Score            *float64        `json:"score"`
		Comment          *string         `json:"comment"`
		Postedit         json.RawMessage `json:"postedit"`
		PosteditApproved *bool           `json:"postedit_approved"`
		PosteditScore    *float64        `json:"postedit_score"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		res.Invalid = true
		return res
	}
	if mode == ModeAPE {
		if p.Postedit == nil {
			res.Invalid = true
			return res
		}
	} else if p.Approved == nil {
		res.Invalid = true
		return res
	}
	// A verdict is read only where one was asked for. A model that volunteers
	// "approved" on an APE call is answering a question the contract does not
	// contain, and acting on it is exactly what this mode must not do.
	if p.Approved != nil && mode != ModeAPE {
		res.Approved = *p.Approved
	}
	res.Score = clampScore(p.Score)
	if p.Comment != nil && strings.TrimSpace(*p.Comment) != "" {
		c := *p.Comment
		res.Comment = &c
	}
	var postedit *string
	if err := json.Unmarshal(p.Postedit, &postedit); err == nil && postedit != nil && strings.TrimSpace(*postedit) != "" {
		res.Postedit = postedit
		if mode != ModeAPE {
			res.PosteditApproved = p.PosteditApproved
			res.PosteditScore = clampScore(p.PosteditScore)
		}
	}
	return res
}

// clampScore bounds a 0-100 score, passing nil through.
func clampScore(s *float64) *float64 {
	if s == nil {
		return nil
	}
	v := *s
	if v < 0 {
		v = 0
	} else if v > 100 {
		v = 100
	}
	return &v
}

// extractJSONObject slices the first '{' to the last '}' of raw, which also
// strips markdown fences and prose wrappers. Empty when no object is present.
func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
