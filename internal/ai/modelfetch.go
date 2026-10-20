package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// Live model lists from each vendor's own API. The catalog
// is what a tenant's key can actually reach, so it is fetched per tenant with
// that tenant's credential rather than compiled in.

// RemoteModel is one row of a provider's model list. DisplayName is empty for
// providers that don't publish one (OpenAI). Lifecycle is Azure OpenAI's
// `lifecycle_status` — "generally-available", "preview", "deprecating" or
// "deprecated" — and is empty everywhere else, since neither OpenAI nor
// Anthropic says anything about retirement in their list.
type RemoteModel struct {
	ID          string
	DisplayName string
	Created     time.Time
	Lifecycle   string
}

// LifecycleDeprecated is the one lifecycle_status that means the model is at or
// past retirement. "deprecating" still serves inference until its end date, so
// it stays offered.
const LifecycleDeprecated = "deprecated"

// ModelLister fetches a vendor's available models with one tenant's credential.
// Injected like ConnectionTester so tests never reach a provider.
type ModelLister interface {
	List(ctx context.Context, provider, apiKey, baseURL string) ([]RemoteModel, error)
}

// modelPageSize is how many rows to ask Anthropic for. Its list is paginated and
// newest-first; one page is far more than the handful we display, so pagination
// is deliberately not followed.
const modelPageSize = 100

// azureModels is the fixed list an Azure endpoint offers, and the reason it is
// fixed rather than fetched:
//
// Azure's `model` field names a DEPLOYMENT, not a model. Its /openai/v1/models
// answers with the models available in the resource's REGION — roughly 200 ids,
// including ones nobody deploys (dall-e-2, babbage) — and no field on a row says
// which of them this resource has actually deployed. So every id the list offers
// is a guess, and one that isn't deployed fails the whole request with
// DeploymentNotFound.
//
// `gpt-5` is the portal's default deployment name for the current model, which
// makes it the one id worth guessing. A customer who named their deployment
// something else still cannot be served from here — listing deployments needs
// the control plane (an Entra token, which a resource key is not).
var azureModels = []RemoteModel{{ID: "gpt-5", DisplayName: "GPT-5"}}

type httpModelLister struct{ client *http.Client }

// NewHTTPModelLister returns the production lister (10s timeout per call, the
// same budget as the connection tester).
func NewHTTPModelLister() ModelLister {
	return &httpModelLister{client: netguard.Client(10 * time.Second)}
}

func (l *httpModelLister) List(ctx context.Context, provider, apiKey, baseURL string) ([]RemoteModel, error) {
	var (
		req *http.Request
		err error
	)
	switch provider {
	case ProviderOpenAI:
		base := orDefault(baseURL, "https://api.openai.com")
		if IsAzureEndpoint(base) {
			return azureModels, nil
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		SetOpenAIAuth(req, apiKey, base)
	case ProviderClaude:
		base := orDefault(baseURL, "https://api.anthropic.com")
		req, err = http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/v1/models?limit=%d", base, modelPageSize), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", anthropicVersion)
	default:
		return nil, fmt.Errorf("provider %q has no model list", provider)
	}
	body, err := doExpectOK(l.client, req)
	if err != nil {
		return nil, err
	}
	models, err := parseModelList(provider, body)
	if err != nil {
		logging.Warn(ctx, err, "provider model list did not parse", "ai-model-list-body",
			logging.Provider(provider),
			logging.Debugf("url", req.URL.String()),
			logging.Debugf("body", string(body)))
		return nil, err
	}
	logging.Info(ctx, "provider model list fetched", "ai-model-list",
		logging.Provider(provider),
		logging.Debugf("url", req.URL.String()),
		logging.Debugf("bytes", len(body)),
		logging.Debugf("models", len(models)))
	return models, nil
}

// modelListResponse covers every vendor shape: OpenAI sends {id, created}
// (Unix seconds), Anthropic {id, display_name, created_at} (RFC 3339), and
// Azure OpenAI {id, created_at} with created_at as Unix seconds. The two
// disagree on the type behind `created_at`, so it is decoded by hand.
type modelListResponse struct {
	Data []struct {
		ID          string          `json:"id"`
		DisplayName string          `json:"display_name"`
		Created     int64           `json:"created"`
		CreatedAt   json.RawMessage `json:"created_at"`
		Lifecycle   string          `json:"lifecycle_status"`
	} `json:"data"`
}

// parseCreatedAt reads a `created_at` that may be an RFC 3339 string or a Unix
// integer. An absent or unreadable value yields the zero time: the date only
// orders the list, so one bad row must not fail the whole document.
func parseCreatedAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC()
		}
		return time.Time{}
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

// parseModelList decodes a vendor response into newest-first RemoteModels.
// Sorting here rather than trusting the vendor: Anthropic documents newest-first
// ordering, OpenAI documents none.
func parseModelList(provider string, body []byte) ([]RemoteModel, error) {
	var resp modelListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%s model list is not valid JSON: %w", provider, err)
	}
	out := make([]RemoteModel, 0, len(resp.Data))
	for _, row := range resp.Data {
		m := RemoteModel{ID: row.ID, DisplayName: row.DisplayName, Lifecycle: row.Lifecycle}
		if row.Created > 0 {
			m.Created = time.Unix(row.Created, 0).UTC()
		} else {
			m.Created = parseCreatedAt(row.CreatedAt)
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}
