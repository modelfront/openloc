package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newProviderStub answers GET .../models with 200 when accept says the request
// carries the good key in the vendor's own auth style, and 401 otherwise.
func newProviderStub(accept func(*http.Request) bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if accept(r) {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
}

// testExtProvider is the vendor of an installed extension.
const testExtProvider = "ext:mockqe"

// Each LLM vendor accepts the key only in its own auth style: OpenAI as a
// Bearer token, Anthropic as x-api-key with an anthropic-version header. A
// check that sent the key the other vendor's way would be rejected as a bad key.
func TestHTTPTester(t *testing.T) {
	for _, tc := range []struct {
		provider string
		accept   func(*http.Request) bool
	}{
		{ProviderOpenAI, func(r *http.Request) bool {
			return r.Header.Get("Authorization") == "Bearer good"
		}},
		{ProviderClaude, func(r *http.Request) bool {
			return r.Header.Get("x-api-key") == "good" &&
				r.Header.Get("anthropic-version") == anthropicVersion
		}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			srv := newProviderStub(tc.accept)
			defer srv.Close()

			tester := NewHTTPTester()
			if err := tester.Test(context.Background(), tc.provider, "good", srv.URL); err != nil {
				t.Errorf("good key: got err=%v, want nil", err)
			}
			if err := tester.Test(context.Background(), tc.provider, "bad", srv.URL); err == nil {
				t.Error("bad key: got nil, want an error")
			}
		})
	}
}

func TestHTTPTesterUnknownProvider(t *testing.T) {
	if err := NewHTTPTester().Test(context.Background(), "deepl", "k", ""); err == nil {
		t.Error("unknown provider must error")
	}
}

// Every realistic paste of an Azure/OpenAI endpoint must land on the one form
// the clients append `/v1/...` to. Each wrong form here is an opaque 404 at
// translation time, which is why normalisation happens on save.
func TestNormalizeBaseURL(t *testing.T) {
	const azure = "https://acme.openai.azure.com"
	cases := []struct{ in, want string }{
		// The three shapes a user actually pastes.
		{azure + "/", azure + "/openai"},                        // Azure portal: bare endpoint
		{azure + "/openai/v1/", azure + "/openai"},              // Microsoft docs: full v1 base
		{"https://api.openai.com/v1", "https://api.openai.com"}, // OpenAI docs
		// Already canonical, and idempotent.
		{azure + "/openai", azure + "/openai"},
		{"https://api.openai.com", "https://api.openai.com"},
		// Whitespace + trailing slashes.
		{"  " + azure + "/openai///  ", azure + "/openai"},
		// Empty means "provider default", not a URL to fix up.
		{"", ""},
		{"   ", ""},
		// A non-Azure host never gets the /openai prefix invented for it.
		{"https://gateway.internal.corp", "https://gateway.internal.corp"},
		// Foundry's alternate hostname is Azure too.
		{"https://res.services.ai.azure.com/", "https://res.services.ai.azure.com/openai"},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL(ProviderOpenAI, c.in); got != c.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An extension's endpoint is used as entered: its path (often /v1) is part of
// the vendor's API, and only the extension knows how to build on it.
func TestNormalizeBaseURL_ExtensionKeepsPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://api.example.com/v1", "https://api.example.com/v1"},
		{" https://api.example.com/v1/ ", "https://api.example.com/v1"},
		{"https://acme.openai.azure.com", "https://acme.openai.azure.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL("ext:acme-qe", c.in); got != c.want {
			t.Errorf("NormalizeBaseURL(ext, %q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	t.Setenv("ENV", "development")
	valid := []string{
		"",
		"https://acme.openai.azure.com/openai",
		"https://api.openai.com",
		"http://localhost:8080", // dev gateways stay allowed
		"http://127.0.0.1:1234",
	}
	for _, v := range valid {
		if msg := ValidateBaseURL(v); msg != "" {
			t.Errorf("ValidateBaseURL(%q) = %q, want valid", v, msg)
		}
	}
	invalid := []string{
		"not-a-url",             // no scheme/host
		"ftp://example.com",     // wrong scheme
		"http://api.openai.com", // plaintext to a remote host leaks the key
		"https://x.openai.azure.com/openai?api-version=2024-12-01-preview", // query would be dropped
	}
	for _, v := range invalid {
		if ValidateBaseURL(v) == "" {
			t.Errorf("ValidateBaseURL(%q) = valid, want rejected", v)
		}
	}
}

// Outside development a plaintext loopback endpoint is refused too: only the
// dev stack runs vendor gateways on this machine.
func TestValidateBaseURL_LoopbackHTTPOnlyInDev(t *testing.T) {
	t.Setenv("ENV", "production")
	for _, v := range []string{"http://localhost:8080", "http://127.0.0.1:1234", "http://[::1]:9000"} {
		if ValidateBaseURL(v) == "" {
			t.Errorf("ValidateBaseURL(%q) = valid outside development, want rejected", v)
		}
	}
	if msg := ValidateBaseURL("https://api.openai.com"); msg != "" {
		t.Errorf("https endpoint rejected outside development: %q", msg)
	}
}

func TestIsAzureEndpoint(t *testing.T) {
	azure := []string{
		"https://acme.openai.azure.com/openai",
		"https://res.services.ai.azure.com",
		"https://RES.OPENAI.AZURE.COM/openai", // case-insensitive host
	}
	for _, u := range azure {
		if !IsAzureEndpoint(u) {
			t.Errorf("IsAzureEndpoint(%q) = false, want true", u)
		}
	}
	notAzure := []string{
		"https://api.openai.com",
		"",
		// The reason this matches on the parsed host: a substring check would
		// call these Azure and leak the resource key header to them.
		"https://evil.com/?x=.openai.azure.com",
		"https://evil.com/.openai.azure.com",
		"https://notopenai.azure.com.evil.com",
	}
	for _, u := range notAzure {
		if IsAzureEndpoint(u) {
			t.Errorf("IsAzureEndpoint(%q) = true, want false", u)
		}
	}
}

// The connection test and the translation call must authenticate identically —
// otherwise a token can verify as "connected" and then fail every translation.
func TestSetOpenAIAuth(t *testing.T) {
	const key = "sk-secret"
	azureReq, _ := http.NewRequest(http.MethodGet, "https://x.openai.azure.com/openai/v1/models", nil)
	SetOpenAIAuth(azureReq, key, "https://x.openai.azure.com/openai")
	if got := azureReq.Header.Get("Authorization"); got != "Bearer "+key {
		t.Errorf("azure Authorization = %q, want bearer", got)
	}
	if got := azureReq.Header.Get("api-key"); got != key {
		t.Errorf("azure api-key = %q, want the key (Azure documents both shapes)", got)
	}

	openaiReq, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	SetOpenAIAuth(openaiReq, key, "https://api.openai.com")
	if got := openaiReq.Header.Get("Authorization"); got != "Bearer "+key {
		t.Errorf("openai Authorization = %q, want bearer", got)
	}
	if openaiReq.Header.Get("api-key") != "" {
		t.Error("openai proper must not receive the azure api-key header")
	}
}

func TestValidProvider(t *testing.T) {
	for _, p := range KnownProviders {
		if !ValidProvider(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	if ValidProvider("gemini") {
		t.Error("unknown provider should be invalid")
	}
}

// DeepL splits Free and Pro across two hostnames, and the KEY says which: Free
// keys carry a ":fx" suffix. Sending a Free key to the Pro host (or the
// reverse) returns 403, which the UI would surface as "your key was rejected"
// for a perfectly good key. Derived from the key rather than asked for, so this
// is the only thing standing between a valid key and a confusing rejection.
func TestDeepLHostFollowsKeyTier(t *testing.T) {
	for _, tc := range []struct {
		name, key, want string
	}{
		{"free key", "abc-123:fx", "https://api-free.deepl.com"},
		{"pro key", "abc-123", "https://api.deepl.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deepLHost(tc.key); got != tc.want {
				t.Errorf("deepLHost(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// The MT vendors authenticate differently from the LLM ones — DeepL by an
// Authorization header with its own scheme, Google by its API-key header. Getting
// either wrong means every key is reported invalid.
func TestTestMTVendorAuth(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		check          func(*http.Request) bool
	}{
		{
			name: "deepl sends the DeepL-Auth-Key scheme", provider: ProviderDeepL,
			check: func(r *http.Request) bool {
				return r.URL.Path == "/v2/usage" &&
					r.Header.Get("Authorization") == "DeepL-Auth-Key good"
			},
		},
		{
			name: "google sends the key in the X-Goog-Api-Key header", provider: ProviderGoogle,
			check: func(r *http.Request) bool {
				return strings.HasSuffix(r.URL.Path, "/language/translate/v2/languages") &&
					r.Header.Get("X-Goog-Api-Key") == "good" && r.URL.RawQuery == ""
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.check(r) {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			tester := NewHTTPTester()
			if err := tester.Test(context.Background(), tc.provider, "good", srv.URL); err != nil {
				t.Errorf("Test() with a good key = %v, want nil", err)
			}
			// A wrong key must be REPORTED, not silently accepted.
			if err := tester.Test(context.Background(), tc.provider, "bad", srv.URL); err == nil {
				t.Error("Test() with a bad key = nil, want an error")
			}
		})
	}
}

// A transport failure's error quotes the request URL, and the key check logs
// that error. The key must not be in it.
func TestKeyCheckTransportErrorOmitsKey(t *testing.T) {
	const key = "test-google-key-0001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	err := NewHTTPTester().Test(context.Background(), ProviderGoogle, key, srv.URL)
	if err == nil {
		t.Fatal("Test() against a dropped connection = nil, want a transport error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("transport error carries the API key: %v", err)
	}
}

// The MT vendors hold a credential but are not text-generation models: an AI
// system drives an LLM with a prompt and they have no such interface. If either
// predicate went true they would show up in AI-engine model pickers with an
// empty catalog, and every run would fail at request time.
func TestMTVendorsCannotBackAnAIEngine(t *testing.T) {
	for _, p := range []string{ProviderDeepL, ProviderGoogle} {
		if !ValidProvider(p) {
			t.Errorf("ValidProvider(%q) = false — it must be configurable in AI settings", p)
		}
		if CanMachineTranslate(p) {
			t.Errorf("CanMachineTranslate(%q) = true — reachable only as an MT engine", p)
		}
		if CanPredict(p) {
			t.Errorf("CanPredict(%q) = true — it cannot follow the QE+APE contract", p)
		}
		if HasModelList(p) {
			t.Errorf("HasModelList(%q) = true — these vendors publish no model list", p)
		}
	}
}

// redirectTransport sends every request to srv, whatever host it names, so a
// real Azure hostname can reach a test server.
type redirectTransport struct{ srv *httptest.Server }

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(t.srv.URL, "http://")
	return http.DefaultTransport.RoundTrip(r)
}

// Azure is checked with a one-line chat call to the fixed gpt-5 deployment,
// never with /models: that list is region-wide, slow, and says nothing about
// what the resource has deployed.
func TestHTTPTesterAzureUsesChatCompletion(t *testing.T) {
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = body.Model
		if r.Header.Get("api-key") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tester := &httpTester{client: &http.Client{Transport: redirectTransport{srv}}}
	const base = "https://acme.openai.azure.com/openai"
	if err := tester.Test(context.Background(), ProviderOpenAI, "good", base); err != nil {
		t.Fatalf("good Azure key: %v", err)
	}
	if gotPath != "POST /openai/v1/chat/completions" || gotModel != "gpt-5" {
		t.Errorf("Azure check sent %q model=%q, want POST /openai/v1/chat/completions model=gpt-5", gotPath, gotModel)
	}
	if err := tester.Test(context.Background(), ProviderOpenAI, "bad", base); err == nil {
		t.Error("bad Azure key must fail")
	}
}
