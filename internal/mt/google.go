package mt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"slices"
)

// Google calls the Google Cloud Translation API v2.
type Google struct {
	APIKey string
	// BaseURL reaches Google through a regional endpoint, gateway or proxy;
	// empty uses the public API. Users set it on the AI settings page.
	BaseURL string
	// Per-engine request options from mt_engines.settings, already validated
	// against the catalog in internal/mtengine. Zero values are not sent, so
	// Google's own defaults apply.
	Options GoogleOptions
}

// GoogleOptions are the tunables an MT engine carries for Google. `format` is
// deliberately absent: it tracks whether the batch carries inline tags, which
// is a property of the segments rather than a preference.
type GoogleOptions struct {
	Model string
}

func (g Google) Name() string { return "google" }

// googleBatchLimit is the max number of texts per Google Translate API request.
const googleBatchLimit = 128

func (g Google) Translate(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	if g.APIKey == "" {
		return nil, fmt.Errorf("Google Translate API key not configured")
	}
	if len(req.Texts) == 0 {
		return nil, nil
	}

	baseURL := g.resolveBaseURL()

	// Chunk into batches of googleBatchLimit
	var allResults []TranslateResult
	for batch := range slices.Chunk(req.Texts, googleBatchLimit) {
		results, err := g.translateBatch(ctx, baseURL, req.SourceLang, req.TargetLang, batch)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}
	return allResults, nil
}

func (g Google) resolveBaseURL() string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return "https://translation.googleapis.com"
}

func (g Google) translateBatch(ctx context.Context, baseURL, srcLang, tgtLang string, texts []string) ([]TranslateResult, error) {
	body := googleRequest{
		Q:      texts,
		Source: srcLang,
		Target: tgtLang,
		Format: "text",
		Model:  g.Options.Model,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/language/translate/v2", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("google: translate endpoint %q is not a valid URL: %w", baseURL, scrubURLError(err))
	}
	httpReq.URL.RawQuery = neturl.Values{"key": {g.APIKey}}.Encode()
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google: request failed: %w", scrubURLError(err))
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("google: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, g.parseError(resp.StatusCode, respBody)
	}

	var googleResp googleResponse
	if err := json.Unmarshal(respBody, &googleResp); err != nil {
		return nil, fmt.Errorf("google: unmarshal response: %w", err)
	}

	if len(googleResp.Data.Translations) != len(texts) {
		return nil, fmt.Errorf("google: response count mismatch: got %d, expected %d", len(googleResp.Data.Translations), len(texts))
	}

	results := make([]TranslateResult, len(googleResp.Data.Translations))
	for i, t := range googleResp.Data.Translations {
		results[i] = TranslateResult{Text: t.TranslatedText}
	}
	return results, nil
}

func (g Google) parseError(statusCode int, body []byte) error {
	// Try to extract Google's error message
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		return fmt.Errorf("google: API error %d: %s", errResp.Error.Code, errResp.Error.Message)
	}

	switch statusCode {
	case 400:
		return fmt.Errorf("google: bad request (400) — check language codes")
	case 403:
		return fmt.Errorf("google: authentication failed (403) — check your API key")
	case 429:
		return fmt.Errorf("google: rate limit exceeded (429) — too many requests")
	default:
		return &apiError{vendor: "google: API", status: statusCode, body: string(body)}
	}
}

type googleRequest struct {
	Q      []string `json:"q"`
	Source string   `json:"source"`
	Target string   `json:"target"`
	Format string   `json:"format"`
	Model  string   `json:"model,omitempty"`
}

type googleResponse struct {
	Data struct {
		Translations []struct {
			TranslatedText string `json:"translatedText"`
		} `json:"translations"`
	} `json:"data"`
}

func (g Google) LanguageEndpoint() string { return g.resolveBaseURL() }

// FetchLanguages reads Google's live catalog. One list serves both roles:
// Google reads and writes the same languages.
func (g Google) FetchLanguages(ctx context.Context) (Catalog, error) {
	if g.APIKey == "" {
		return Catalog{}, errNoCredential
	}
	// The key goes on via RawQuery, never into the string handed to url.Parse:
	// a parse failure returns a *url.Error quoting the whole raw URL, and that
	// error is wrapped into a log line.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.resolveBaseURL()+"/language/translate/v2/languages", nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("google: languages endpoint %q is not a valid URL: %w", g.resolveBaseURL(), scrubURLError(err))
	}
	req.URL.RawQuery = neturl.Values{"target": {"en"}, "key": {g.APIKey}}.Encode()
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return Catalog{}, fmt.Errorf("google: languages request: %w", scrubURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("google: languages returned %d", resp.StatusCode)
	}
	body, err := readLanguageList(resp.Body)
	if err != nil {
		return Catalog{}, fmt.Errorf("google: read languages: %w", err)
	}
	var parsed struct {
		Data struct {
			Languages []struct {
				Language string `json:"language"`
				Name     string `json:"name"`
			} `json:"languages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Catalog{}, fmt.Errorf("google: languages is not valid JSON: %w", err)
	}
	rows := parsed.Data.Languages
	if len(rows) == 0 {
		return Catalog{}, fmt.Errorf("google: empty language list")
	}
	if len(rows) > maxLanguageEntries {
		return Catalog{}, fmt.Errorf("google: language list declares %d languages, refusing above %d", len(rows), maxLanguageEntries)
	}

	langs := make([]LanguageInfo, 0, len(rows))
	for _, r := range rows {
		code, ok := normalizeCode(r.Language)
		if !ok {
			return Catalog{}, fmt.Errorf("google: language list carries a code of %d characters, refusing above %d", len(r.Language), maxLanguageCodeLen)
		}
		langs = append(langs, LanguageInfo{Code: code, Name: truncateName(r.Name)})
	}
	return symmetric(langs), nil
}
