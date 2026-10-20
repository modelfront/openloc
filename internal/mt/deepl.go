package mt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// DeepL calls the DeepL API for machine translation.
// Supports both free (api-free.deepl.com) and pro (api.deepl.com) endpoints.
type DeepL struct {
	APIKey string
	// BaseURL reaches DeepL through a gateway or proxy; empty picks the Free or
	// Pro host from the key. Users set it on the AI settings page.
	BaseURL string
	// Per-engine request options from mt_engines.settings, already validated
	// against the catalog in internal/mtengine. Zero values are not sent, so
	// DeepL's own defaults apply.
	Options DeepLOptions
}

// DeepLOptions are the tunables an MT engine carries for DeepL. Pointers where
// the provider's default is not the zero value: `preserve_formatting=false` is
// a real instruction, distinct from not sending the field.
type DeepLOptions struct {
	Formality          string
	GlossaryID         string
	PreserveFormatting *bool
	SplitSentences     string
	ModelType          string
}

func (d DeepL) Name() string { return "deepl" }

// deeplBatchLimit is the max number of texts per DeepL API request.
const deeplBatchLimit = 50

func (d DeepL) Translate(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	return d.translate(ctx, req, "")
}

// TranslateTagged sends the texts with DeepL's XML tag handling enabled so
// inline XLIFF tags (`<g>`, `<x>`, …) are preserved and repositioned around
// the translated content. outline_detection is disabled
// because our tags are always inline formatting, never document structure —
// letting DeepL infer structure would risk splitting on `<g>`.
func (d DeepL) TranslateTagged(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	return d.translate(ctx, req, "xml")
}

// translate is the shared batch driver. tagHandling is "" for plain text or
// "xml" for tag-preserving mode.
func (d DeepL) translate(ctx context.Context, req TranslateRequest, tagHandling string) ([]TranslateResult, error) {
	if d.APIKey == "" {
		return nil, fmt.Errorf("DeepL API key not configured")
	}
	if len(req.Texts) == 0 {
		return nil, nil
	}

	baseURL := d.resolveBaseURL()

	// Chunk into batches of deeplBatchLimit
	var allResults []TranslateResult
	for batch := range slices.Chunk(req.Texts, deeplBatchLimit) {
		results, err := d.translateBatch(ctx, baseURL, req.SourceLang, req.TargetLang, batch, tagHandling)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}
	return allResults, nil
}

func (d DeepL) resolveBaseURL() string {
	if d.BaseURL != "" {
		return d.BaseURL
	}
	// Free API keys end with ":fx"
	if strings.HasSuffix(d.APIKey, ":fx") {
		return "https://api-free.deepl.com"
	}
	return "https://api.deepl.com"
}

func (d DeepL) translateBatch(ctx context.Context, baseURL, srcLang, tgtLang string, texts []string, tagHandling string) ([]TranslateResult, error) {
	body := deeplRequest{
		Text:        texts,
		SourceLang:  deeplLangCode(srcLang, false),
		TargetLang:  deeplLangCode(tgtLang, true),
		TagHandling: tagHandling,
		// The engine's own options. tag_handling and outline_detection are NOT
		// among them: they track whether this batch carries inline tags, which
		// is a property of the segments, not a preference.
		Formality:          d.Options.Formality,
		GlossaryID:         d.Options.GlossaryID,
		PreserveFormatting: d.Options.PreserveFormatting,
		SplitSentences:     d.Options.SplitSentences,
		ModelType:          d.Options.ModelType,
	}
	if tagHandling == "xml" {
		// Our tags are always inline formatting, never document structure.
		// Disable outline detection so DeepL treats <g>/<x>/… as inline and
		// repositions them, instead of inferring splitting structure.
		outlineOff := false
		body.OutlineDetection = &outlineOff
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("deepl: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v2/translate", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("deepl: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "DeepL-Auth-Key "+d.APIKey)

	resp, err := HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("deepl: request failed: %w", err)
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("deepl: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, d.parseError(resp.StatusCode, respBody)
	}

	var deeplResp deeplResponse
	if err := json.Unmarshal(respBody, &deeplResp); err != nil {
		return nil, fmt.Errorf("deepl: unmarshal response: %w", err)
	}

	if len(deeplResp.Translations) != len(texts) {
		return nil, fmt.Errorf("deepl: response count mismatch: got %d, expected %d", len(deeplResp.Translations), len(texts))
	}

	results := make([]TranslateResult, len(deeplResp.Translations))
	for i, t := range deeplResp.Translations {
		results[i] = TranslateResult{Text: t.Text}
	}
	return results, nil
}

func (d DeepL) parseError(statusCode int, body []byte) error {
	switch statusCode {
	case 403:
		return fmt.Errorf("deepl: authentication failed (403) — check your API key")
	case 429:
		return fmt.Errorf("deepl: rate limit exceeded (429) — too many requests")
	case 456:
		return fmt.Errorf("deepl: quota exceeded (456) — translation limit reached")
	default:
		return &apiError{vendor: "deepl: API", status: statusCode, body: string(body)}
	}
}

// deeplLangCode converts our normalized lowercase codes to DeepL's format.
// DeepL uses uppercase and distinguishes some regional variants for targets.
func deeplLangCode(code string, isTarget bool) string {
	upper := strings.ToUpper(code)

	if isTarget {
		// DeepL requires regional variants for certain target languages
		switch upper {
		case "EN":
			return "EN-US" // default to American English
		case "PT":
			return "PT-PT" // default to European Portuguese
		}
	}
	return upper
}

type deeplRequest struct {
	Text             []string `json:"text"`
	SourceLang       string   `json:"source_lang"`
	TargetLang       string   `json:"target_lang"`
	TagHandling      string   `json:"tag_handling,omitempty"`
	OutlineDetection *bool    `json:"outline_detection,omitempty"`
	Formality        string   `json:"formality,omitempty"`
	GlossaryID       string   `json:"glossary_id,omitempty"`
	// Pointer + omitempty: false must reach DeepL when chosen, and be absent
	// when not chosen. A plain bool cannot say both.
	PreserveFormatting *bool  `json:"preserve_formatting,omitempty"`
	SplitSentences     string `json:"split_sentences,omitempty"`
	ModelType          string `json:"model_type,omitempty"`
}

type deeplResponse struct {
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

func (d DeepL) LanguageEndpoint() string { return d.resolveBaseURL() }

// FetchLanguages reads DeepL's live catalog. Source and target are two separate
// calls because DeepL's lists differ: EN and PT are source-only, EN-GB, EN-US,
// PT-BR and PT-PT target-only. They are stored as fetched — the asymmetry is
// between the two lists, so nothing has to be joined.
func (d DeepL) FetchLanguages(ctx context.Context) (Catalog, error) {
	if d.APIKey == "" {
		return Catalog{}, errNoCredential
	}
	baseURL := d.resolveBaseURL()
	sources, err := d.fetchLanguageList(ctx, baseURL, "source")
	if err != nil {
		return Catalog{}, err
	}
	targets, err := d.fetchLanguageList(ctx, baseURL, "target")
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{Sources: withBaseCodes(sources), Targets: withBaseCodes(targets)}, nil
}

func (d DeepL) fetchLanguageList(ctx context.Context, baseURL, listType string) ([]LanguageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v2/languages?type="+listType, nil)
	if err != nil {
		return nil, fmt.Errorf("deepl: languages endpoint %q is not a valid URL: %w", baseURL, err)
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+d.APIKey)

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepl: %s languages request: %w", listType, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepl: %s languages returned %d", listType, resp.StatusCode)
	}
	body, err := readLanguageList(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("deepl: read %s languages: %w", listType, err)
	}
	var rows []struct {
		Language string `json:"language"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("deepl: %s languages is not valid JSON: %w", listType, err)
	}
	if len(rows) > maxLanguageEntries {
		return nil, fmt.Errorf("deepl: %s list declares %d languages, refusing above %d", listType, len(rows), maxLanguageEntries)
	}
	out := make([]LanguageInfo, 0, len(rows))
	for _, r := range rows {
		code, ok := normalizeCode(r.Language)
		if !ok {
			return nil, fmt.Errorf("deepl: %s list carries a code of %d characters, refusing above %d", listType, len(r.Language), maxLanguageCodeLen)
		}
		out = append(out, LanguageInfo{Code: code, Name: truncateName(r.Name)})
	}
	return out, nil
}
