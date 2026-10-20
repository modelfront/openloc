package mt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"openloc/internal/langcode"
	"openloc/internal/logging"
)

// LibreTranslate calls a self-hosted LibreTranslate instance.
type LibreTranslate struct {
	APIURL string // e.g. "http://localhost:5100"
	APIKey string // optional, depends on instance config
}

func (lt LibreTranslate) Name() string { return "libretranslate" }

func (lt LibreTranslate) Translate(ctx context.Context, req TranslateRequest) ([]TranslateResult, error) {
	baseURL := lt.resolveBaseURL()

	if len(req.Texts) == 0 {
		return nil, nil
	}

	// Try batch mode first (q = array of strings), fall back to one-by-one for
	// pre-1.3 LibreTranslate. Log the batch error so the fallback path isn't silent —
	// without this, count-mismatches or 4xx on rich-text payloads disappear.
	results, err := lt.translateBatch(ctx, baseURL, req)
	if err != nil {
		logging.Warn(ctx, err, "LibreTranslate batch failed; falling back to one-by-one", "mt-libretranslate-batch-failed",
			append(logging.Langs(req.SourceLang, req.TargetLang), logging.Count(len(req.Texts)))...)
		return lt.translateOneByOne(ctx, baseURL, req)
	}
	return results, nil
}

// translateBatch sends all texts as a JSON array in the "q" field.
// LibreTranslate returns {"translatedText": ["...", "..."]} for array input.
func (lt LibreTranslate) translateBatch(ctx context.Context, baseURL string, req TranslateRequest) ([]TranslateResult, error) {
	body := ltBatchRequest{
		Q:      req.Texts,
		Source: req.SourceLang,
		Target: req.TargetLang,
		APIKey: lt.APIKey,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/translate", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := operatorClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("translate request failed: %w", err)
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &apiError{vendor: "libretranslate", status: resp.StatusCode, body: string(respBody)}
	}

	var batchResp ltBatchResponse
	if err := json.Unmarshal(respBody, &batchResp); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}

	if len(batchResp.TranslatedText) != len(req.Texts) {
		return nil, fmt.Errorf("batch response count mismatch: got %d, expected %d", len(batchResp.TranslatedText), len(req.Texts))
	}

	results := make([]TranslateResult, len(batchResp.TranslatedText))
	for i, text := range batchResp.TranslatedText {
		results[i] = TranslateResult{Text: text}
	}
	return results, nil
}

// translateOneByOne sends one request per text (fallback for older instances).
func (lt LibreTranslate) translateOneByOne(ctx context.Context, baseURL string, req TranslateRequest) ([]TranslateResult, error) {
	results := make([]TranslateResult, len(req.Texts))
	for i, text := range req.Texts {
		body := ltSingleRequest{
			Q:      text,
			Source: req.SourceLang,
			Target: req.TargetLang,
			APIKey: lt.APIKey,
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/translate", bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := operatorClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("translate request failed: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, &apiError{vendor: "libretranslate", status: resp.StatusCode, body: string(respBody)}
		}

		var ltResp ltSingleResponse
		if err := json.Unmarshal(respBody, &ltResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		results[i] = TranslateResult{Text: ltResp.TranslatedText}
	}

	return results, nil
}

type ltBatchRequest struct {
	Q      []string `json:"q"`
	Source string   `json:"source"`
	Target string   `json:"target"`
	APIKey string   `json:"api_key,omitempty"`
}

type ltBatchResponse struct {
	TranslatedText []string `json:"translatedText"`
}

type ltSingleRequest struct {
	Q      string `json:"q"`
	Source string `json:"source"`
	Target string `json:"target"`
	APIKey string `json:"api_key,omitempty"`
}

type ltSingleResponse struct {
	TranslatedText string `json:"translatedText"`
}

func (lt LibreTranslate) LanguageEndpoint() string { return lt.resolveBaseURL() }

func (lt LibreTranslate) resolveBaseURL() string {
	if lt.APIURL != "" {
		return lt.APIURL
	}
	return "http://localhost:5100"
}

// FetchLanguages reads the instance's catalog. LibreTranslate is the one engine
// that publishes a different target list per source, so it is also the only one
// that fills Catalog.PerSource.
func (lt LibreTranslate) FetchLanguages(ctx context.Context) (Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lt.resolveBaseURL()+"/languages", nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("libretranslate: languages endpoint %q is not a valid URL: %w", lt.resolveBaseURL(), err)
	}
	resp, err := operatorClient.Do(req)
	if err != nil {
		return Catalog{}, fmt.Errorf("libretranslate: languages request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("libretranslate: languages returned %d", resp.StatusCode)
	}
	body, err := readLanguageList(resp.Body)
	if err != nil {
		return Catalog{}, fmt.Errorf("libretranslate: read languages: %w", err)
	}
	var rows []struct {
		Code    string   `json:"code"`
		Name    string   `json:"name"`
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return Catalog{}, fmt.Errorf("libretranslate: languages is not valid JSON: %w", err)
	}
	if len(rows) == 0 {
		return Catalog{}, fmt.Errorf("libretranslate: empty language list")
	}
	// The same bound the other engines carry: this one publishes per-source
	// targets, so an oversized list costs more here, not less.
	if len(rows) > maxLanguageEntries {
		return Catalog{}, fmt.Errorf("libretranslate: languages declare %d entries, refusing above %d", len(rows), maxLanguageEntries)
	}

	cat := Catalog{PerSource: make(map[string][]string, len(rows))}
	targetSeen := map[string]bool{}
	// A target code repeats once per source that reaches it, and normalizing
	// allocates a fresh string each time. interned hands every repeat the first
	// one, so a fully-connected catalog holds one allocation per distinct code
	// rather than one per pair.
	interned := map[string]string{}
	perSourceTotal := 0
	for _, r := range rows {
		code, ok := normalizeCode(r.Code)
		if !ok {
			return Catalog{}, fmt.Errorf("libretranslate: languages carry a code of %d characters, refusing above %d", len(r.Code), maxLanguageCodeLen)
		}
		cat.Sources = append(cat.Sources, LanguageInfo{Code: code, Name: truncateName(r.Name)})

		// Targets get the same treatment the source side gets. They are not
		// exempt for being "linear in an already-capped body": each distinct
		// target is retained once in PerSource and twice in Targets, so a body
		// of short codes amplifies several times over on the way into the cache.
		// A bad entry is skipped rather than failing the fetch — one malformed
		// code should not cost the whole catalog.
		targets := make([]string, 0, min(len(r.Targets), maxLanguageEntries))
		rowSeen := map[string]bool{}
		for _, raw := range langcode.NormalizeAll(r.Targets) {
			t, ok := normalizeCode(raw)
			if !ok {
				continue
			}
			if rowSeen[t] {
				continue
			}
			rowSeen[t] = true
			if first, ok := interned[t]; ok {
				t = first
			} else {
				interned[t] = t
			}
			perSourceTotal++
			if perSourceTotal > maxPerSourceTargets {
				return Catalog{}, fmt.Errorf("libretranslate: languages declare over %d per-source targets, refusing", maxPerSourceTargets)
			}
			targets = append(targets, t)
			if targetSeen[t] || len(cat.Targets) >= maxLanguageEntries {
				continue
			}
			targetSeen[t] = true
			cat.Targets = append(cat.Targets, LanguageInfo{Code: t, Name: t})
		}
		cat.PerSource[code] = targets
	}

	// A base code derived from a regional source needs an entry of its own:
	// targetSet keys on PerSource and returns nil on a miss, which would refuse
	// a language the catalog does cover. It reaches the union of what its
	// regional forms reach.
	cat.Sources = withBaseCodes(cat.Sources)
	for _, src := range cat.Sources {
		if _, ok := cat.PerSource[src.Code]; ok {
			continue
		}
		seen := map[string]bool{}
		var union []string
		for code, targets := range cat.PerSource {
			if baseCode(code) != src.Code {
				continue
			}
			for _, t := range targets {
				if seen[t] {
					continue
				}
				seen[t] = true
				// Counted against the same budget the rows were: these are
				// slots in the same map, so leaving them out would let a
				// catalog of regional codes double what the cap allows.
				perSourceTotal++
				if perSourceTotal > maxPerSourceTargets {
					return Catalog{}, fmt.Errorf("libretranslate: languages declare over %d per-source targets, refusing", maxPerSourceTargets)
				}
				union = append(union, t)
			}
		}
		cat.PerSource[src.Code] = union
	}
	cat.Targets = withBaseCodes(cat.Targets)
	return cat, nil
}
