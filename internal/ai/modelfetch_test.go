package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Payloads shaped like the real responses, including the specialized models the
// listing filter has to remove.
const openAIModelsJSON = `{"object":"list","data":[
  {"id":"gpt-4o","object":"model","created":1715367049,"owned_by":"system"},
  {"id":"gpt-5","object":"model","created":1754425777,"owned_by":"system"},
  {"id":"dall-e-3","object":"model","created":1698785189,"owned_by":"system"},
  {"id":"text-embedding-3-large","object":"model","created":1705953180,"owned_by":"system"},
  {"id":"gpt-4o-2024-08-06","object":"model","created":1722814719,"owned_by":"system"}
]}`

const claudeModelsJSON = `{"data":[
  {"type":"model","id":"claude-opus-5","display_name":"Claude Opus 5","created_at":"2026-02-05T00:00:00Z"},
  {"type":"model","id":"claude-sonnet-5","display_name":"Claude Sonnet 5","created_at":"2026-01-05T00:00:00Z"}
],"has_more":false}`

func TestListOpenAIModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openAIModelsJSON))
	}))
	defer srv.Close()

	got, err := NewHTTPModelLister().List(context.Background(), ProviderOpenAI, "sk-test", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}
	// Newest first, regardless of the order the provider sent.
	if len(got) != 5 || got[0].ID != "gpt-5" {
		t.Fatalf("want newest-first with gpt-5 leading, got %v", remoteIDs(got))
	}
	// The listing filter runs on top of the raw fetch.
	models := classifyAll(ProviderOpenAI, got)
	want := []string{"gpt-5", "gpt-4o"}
	if diff := ids(models); len(diff) != len(want) || diff[0] != want[0] || diff[1] != want[1] {
		t.Errorf("classified = %v, want %v (image + embedding dropped, dated snapshot collapsed)", diff, want)
	}
}

func TestListClaudeModels(t *testing.T) {
	var gotKey, gotVersion, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(claudeModelsJSON))
	}))
	defer srv.Close()

	got, err := NewHTTPModelLister().List(context.Background(), ProviderClaude, "sk-ant", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "sk-ant" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	// Anthropic rejects a request without its version header.
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if gotQuery != "limit=100" {
		t.Errorf("query = %q, want limit=100", gotQuery)
	}
	if len(got) != 2 || got[0].ID != "claude-opus-5" || got[0].DisplayName != "Claude Opus 5" {
		t.Fatalf("unexpected models: %+v", got)
	}
}

// An Azure resource takes the key as `api-key` in its REST samples and as a
// bearer in its SDK samples; a token that lists models under one shape and
// translates under the other would pass setup and fail in production.
func TestListOpenAIModelsAzureSendsBothAuthHeaders(t *testing.T) {
	var bearer, apiKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, apiKey = r.Header.Get("Authorization"), r.Header.Get("api-key")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	// SetOpenAIAuth keys off the base URL, so assert on the helper the lister
	// shares with the connection tester rather than on a fake Azure hostname.
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetOpenAIAuth(req, "resource-key", "https://acme.openai.azure.com/openai")
	if _, err := doExpectOK(srv.Client(), req); err != nil {
		t.Fatal(err)
	}
	if bearer != "Bearer resource-key" || apiKey != "resource-key" {
		t.Errorf("Azure request must carry both auth shapes, got Authorization=%q api-key=%q", bearer, apiKey)
	}
}

// refuseTransport fails any request that reaches it, naming the URL.
type refuseTransport struct{ t *testing.T }

func (r refuseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("unexpected request to %s", req.URL)
	return nil, fmt.Errorf("no requests allowed")
}

// Azure is served a fixed list, with no call at all: its /openai/v1/models is
// the region's catalog, and picking an id out of it that the resource has not
// deployed fails every request with DeploymentNotFound.
func TestListOpenAIModelsAzureIsFixed(t *testing.T) {
	// A transport rather than a test server: the lister builds the URL from
	// baseURL, so an httptest server would sit there unused while the call went
	// to the real host.
	l := &httpModelLister{client: &http.Client{Transport: refuseTransport{t}}}
	got, err := l.List(context.Background(), ProviderOpenAI, "resource-key", "https://acme.openai.azure.com/openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "gpt-5" {
		t.Fatalf("azure models = %+v, want just gpt-5", got)
	}
}

// The fixed id has to classify as a reasoning model: that is what decides the
// request parameters, and the wrong set is a 400 on every segment.
func TestAzureFixedModelIsSelectableAndReasoning(t *testing.T) {
	models := classifyAll(ProviderOpenAI, azureModels)
	if len(models) != 1 {
		t.Fatalf("the listing filter dropped the fixed Azure model: %+v", models)
	}
	if models[0].family != familyOpenAIReasoning {
		t.Errorf("family = %q, want %q", models[0].family, familyOpenAIReasoning)
	}
}

// A rejected key must read as a key problem, not as "this vendor has no models"
// — the refresher keeps the previous list on an error and would silently serve
// a stale one forever if this returned an empty success.
func TestListModelsRejectedKeyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewHTTPModelLister().List(context.Background(), ProviderOpenAI, "sk-bad", srv.URL)
	if err == nil {
		t.Fatal("a 401 must be an error")
	}
}

func TestListModelsUnsupportedProvider(t *testing.T) {
	if _, err := NewHTTPModelLister().List(context.Background(), ProviderDeepL, "k", ""); err == nil {
		t.Fatal("DeepL publishes no model list; List must say so rather than call a URL")
	}
}

func remoteIDs(models []RemoteModel) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

// Azure's /openai/v1/models: `created_at` is a Unix int, not the RFC 3339
// string Anthropic sends on the same key, and rows carry a capabilities block.
const azureModelsJSON = `{"data":[
 {"status":"succeeded","capabilities":{"inference":true,"chat_completion":false},
  "lifecycle_status":"deprecated","id":"dall-e-3-3.0","created_at":1691712000,"object":"model"},
 {"status":"succeeded","capabilities":{"inference":true,"chat_completion":false},
  "lifecycle_status":"generally-available","id":"whisper-001","created_at":1694649600,"object":"model"},
 {"status":"succeeded","capabilities":{"inference":true,"chat_completion":true},
  "lifecycle_status":"deprecating","id":"gpt-4o-2024-05-13","created_at":1715558400,"object":"model"},
 {"status":"succeeded","capabilities":{"inference":true,"chat_completion":true},
  "lifecycle_status":"deprecating","id":"gpt-4o-2024-08-06","created_at":1722902400,"object":"model"}
]}`

func TestParseAzureModelList(t *testing.T) {
	got, err := parseModelList(ProviderOpenAI, []byte(azureModelsJSON))
	if err != nil {
		t.Fatalf("Azure model list must parse: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d models, want 4", len(got))
	}
	if got[0].ID != "gpt-4o-2024-08-06" {
		t.Errorf("newest-first broken: got %q", got[0].ID)
	}
	if got[0].Created.IsZero() {
		t.Errorf("created_at was not read: %+v", got[0])
	}
}

// Azure publishes a retirement state that OpenAI and Anthropic do not. A model
// past it must not be offered, but a system already pinned to one must still
// save — so it stays valid.
func TestDeprecatedAzureModelIsHiddenButStaysValid(t *testing.T) {
	const body = `{"data":[
	 {"id":"gpt-4-0314","created_at":1679356800,"lifecycle_status":"deprecated"},
	 {"id":"gpt-4o-2024-05-13","created_at":1715558400,"lifecycle_status":"deprecating"},
	 {"id":"gpt-4o-2024-08-06","created_at":1722902400,"lifecycle_status":"generally-available"}
	]}`
	remote, err := parseModelList(ProviderOpenAI, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	c := NewCatalog(nil, nil, DefaultModelLimit)
	c.Set(1, ProviderOpenAI, classifyAll(ProviderOpenAI, remote))

	got := ids(c.Models(context.Background(), 1, ProviderOpenAI))
	for _, id := range got {
		if id == "gpt-4-0314" {
			t.Errorf("a deprecated model is offered in the picker: %v", got)
		}
	}
	// "deprecating" still serves inference until its end date, so it stays.
	if len(got) != 2 {
		t.Errorf("models = %v, want the two still-serving ones", got)
	}
	// Hiding it must not break an AI engine already pinned to it.
	if !c.Valid(context.Background(), 1, ProviderOpenAI, "gpt-4-0314") {
		t.Error("a deprecated model must stay valid — hiding it should not block saving a row that uses it")
	}
}

// OpenAI and Anthropic never send `lifecycle_status`. The key is absent, not
// empty — and for a string field those are the same value, because Unmarshal
// leaves a key it cannot find untouched on an already-zeroed struct. This pins
// that, including the case that would break if the decoder reused a row: a body
// where one model carries the field and the next does not.
func TestAbsentLifecycleIsNotDeprecated(t *testing.T) {
	const mixed = `{"data":[
	 {"id":"gpt-4-0314","created_at":1679356800,"lifecycle_status":"deprecated"},
	 {"id":"gpt-4o","created":1715367049},
	 {"id":"gpt-5","created":1754425777}
	]}`
	remote, err := parseModelList(ProviderOpenAI, []byte(mixed))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RemoteModel{}
	for _, r := range remote {
		byID[r.ID] = r
	}
	// The row that follows a "deprecated" one must not inherit it.
	for _, id := range []string{"gpt-4o", "gpt-5"} {
		if got := byID[id].Lifecycle; got != "" {
			t.Errorf("%s has no lifecycle_status in the body, got %q", id, got)
		}
	}
	if byID["gpt-4-0314"].Lifecycle != LifecycleDeprecated {
		t.Errorf("the row that DOES carry the field lost it: %q", byID["gpt-4-0314"].Lifecycle)
	}

	c := NewCatalog(nil, nil, DefaultModelLimit)
	c.Set(1, ProviderOpenAI, classifyAll(ProviderOpenAI, remote))
	got := ids(c.Models(context.Background(), 1, ProviderOpenAI))
	if len(got) != 2 {
		t.Fatalf("models = %v, want the two without a lifecycle field", got)
	}
	for _, id := range got {
		if id == "gpt-4-0314" {
			t.Errorf("the deprecated model is offered: %v", got)
		}
	}
}
