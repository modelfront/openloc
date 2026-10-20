package ai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"openloc/internal/extension"
	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// Provider identifiers surfaced in the AI tab.
const (
	ProviderOpenAI = "openai"
	ProviderClaude = "claude"
	// Machine-translation vendors. They hold a credential here like any other
	// vendor, but they are NOT text-generation models: they cannot back an AI
	// system, and they are reached through the MT engine path, not this one.
	ProviderDeepL  = "deepl"
	ProviderGoogle = "google"
)

// KnownProviders is the fixed set the AI tab manages, in display order.
// Installed extensions are listed after them.
var KnownProviders = []string{
	ProviderOpenAI, ProviderClaude,
	ProviderDeepL, ProviderGoogle,
}

// ValidProvider reports whether p is one of the known providers.
func ValidProvider(p string) bool {
	for _, k := range KnownProviders {
		if p == k {
			return true
		}
	}
	return false
}

// CanMachineTranslate reports whether provider p can generate a translation
// from scratch (AI-as-MT). An extension only reviews translations, so it
// cannot back an AI engine of type 'mt'. Only text-generation providers
// qualify.
//
// DeepL and Google translate, obviously, but not through THIS path: an AI
// system drives an LLM with a prompt, and they have no such interface. They
// reach the pipeline as MT engines instead, so they must stay false here or
// they would appear in AI-engine model pickers with an empty model list.
func CanMachineTranslate(p string) bool {
	return p == ProviderOpenAI || p == ProviderClaude
}

// IsMTEngineProvider reports whether p is a machine-translation vendor — one
// whose key is spent by an mt_engines row rather than by an AI engine.
// These vendors hold a credential here like any other, but
// the translate pipeline reaches them through the MT engine that names the key.
func IsMTEngineProvider(p string) bool {
	return p == ProviderDeepL || p == ProviderGoogle
}

// CanPredict reports whether provider p can run LLM-backed QE+APE —
// i.e. follow the {status, postedit, score} output contract. An extension
// runs QE+APE through its own predict function, not this contract.
func CanPredict(p string) bool {
	return p == ProviderOpenAI || p == ProviderClaude
}

// ConnectionTester verifies a provider API key by making a lightweight
// authenticated request. Returns nil if the credentials work; a descriptive
// error otherwise. Injected so tests can supply a fake (no external calls).
type ConnectionTester interface {
	Test(ctx context.Context, provider, apiKey, baseURL string) error
}

// httpTester is the production tester — real calls to each provider's API.
type httpTester struct{ client *http.Client }

// NewHTTPTester returns the real connection tester (10s timeout per call).
func NewHTTPTester() ConnectionTester {
	return &httpTester{client: netguard.Client(10 * time.Second)}
}

func (t *httpTester) Test(ctx context.Context, provider, apiKey, baseURL string) error {
	switch provider {
	case ProviderOpenAI:
		base := orDefault(baseURL, "https://api.openai.com")
		if IsAzureEndpoint(base) {
			return t.testAzure(ctx, apiKey, base)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
		if err != nil {
			return err
		}
		SetOpenAIAuth(req, apiKey, base)
		return t.expectOK(req)
	case ProviderClaude:
		base := orDefault(baseURL, "https://api.anthropic.com")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
		if err != nil {
			return err
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", anthropicVersion)
		return t.expectOK(req)
	case ProviderDeepL:
		// DeepL splits Free and Pro across two hostnames and the key itself says
		// which: Free keys carry the ":fx" suffix. Guessing wrong returns 403,
		// which would read as "bad key" for a perfectly good one.
		base := orDefault(baseURL, deepLHost(apiKey))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/usage", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "DeepL-Auth-Key "+apiKey)
		return t.expectOK(req)
	case ProviderGoogle:
		// `languages` is the cheapest authenticated call Cloud Translation v2
		// offers. The key goes in a header, not the `key` query param: a
		// transport error quotes the URL, and that error is logged.
		base := orDefault(baseURL, "https://translation.googleapis.com")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/language/translate/v2/languages", nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Goog-Api-Key", apiKey)
		return t.expectOK(req)
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
}

// azureProbe is the smallest chat request the fixed Azure deployment accepts.
// A 2xx proves the key AND that the deployment exists; a length cut-off still
// answers 200, so the tiny token cap only bounds cost.
var azureProbe = []byte(fmt.Sprintf(
	`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_completion_tokens":16}`,
	azureModels[0].ID))

// testAzure checks an Azure key with one chat call instead of /models, which
// lists the whole region and is not what the resource serves (see azureModels).
func (t *httpTester) testAzure(ctx context.Context, apiKey, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(azureProbe))
	if err != nil {
		return err
	}
	SetOpenAIAuth(req, apiKey, base)
	req.Header.Set("Content-Type", "application/json")
	return t.expectOK(req)
}

// deepLHost picks the API host a DeepL key belongs to. Free keys end in ":fx"
// and only work against api-free; Pro keys only against api. This is a property
// of the key, so it is derived rather than asked for.
func deepLHost(apiKey string) string {
	if strings.HasSuffix(apiKey, ":fx") {
		return "https://api-free.deepl.com"
	}
	return "https://api.deepl.com"
}

// expectOK executes req and maps the response to a connection verdict: 2xx =
// connected; 401/403 = bad key; anything else = a descriptive failure.
func (t *httpTester) expectOK(req *http.Request) error {
	_, err := doExpectOK(t.client, req)
	return err
}

// anthropicVersion is the API version header every Anthropic request must
// carry. It pins the wire format, so it changes only alongside a deliberate
// migration — not with each new model.
const anthropicVersion = "2023-06-01"

// callFailure is a provider call that didn't return 2xx. It carries the status
// so a caller can classify the failure without parsing the message — the
// message itself quotes the provider's body and must not reach a client.
type callFailure struct {
	status    int // 0 for a transport failure
	transport bool
	msg       string
}

var _ logging.LogSafe = (*callFailure)(nil)

func (e *callFailure) Error() string { return e.msg }

// LogMessage implements logging.LogSafe: msg can quote the provider body.
func (e *callFailure) LogMessage() string { return e.Reason() }

// Reason maps the failure to a short, client-safe sentence. Deliberately says
// nothing the provider said: the body can carry account details, and this text
// is rendered in the UI.
func (e *callFailure) Reason() string {
	switch {
	case e.transport:
		return "Could not reach the provider."
	case e.status == http.StatusUnauthorized || e.status == http.StatusForbidden:
		return "The API key was rejected."
	case e.status == http.StatusTooManyRequests:
		return "The provider is rate limiting this key."
	default:
		return fmt.Sprintf("The provider returned an error (status %d).", e.status)
	}
}

// doExpectOK executes req and returns the response body on 2xx. Non-2xx becomes
// a *callFailure describing the problem the way a user can act on it: 401/403 is
// a key problem, anything else quotes the provider. The body is capped — a
// provider error page can be arbitrarily large.
func doExpectOK(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, &callFailure{transport: true, msg: fmt.Sprintf("could not reach provider: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &callFailure{
			status: resp.StatusCode,
			msg:    fmt.Sprintf("invalid API key (status %d)", resp.StatusCode),
		}
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return nil, &callFailure{
		status: resp.StatusCode,
		msg:    fmt.Sprintf("provider returned status %d: %s", resp.StatusCode, string(b)),
	}
}

// failureReason is the client-safe sentence for any error out of a provider
// call, for surfaces that show the user why a model list is empty. Anything not
// a *callFailure gets a generic line rather than its raw text.
func failureReason(err error) string {
	var f *callFailure
	if errors.As(err, &f) {
		return f.Reason()
	}
	return "The model list could not be loaded."
}

// keyCheckReason is the sentence a user sees when a live key check fails: the
// vendor's name and one next step, never the provider's body.
func keyCheckReason(provider string, err error) string {
	name := DisplayName(provider)
	var f *callFailure
	if !errors.As(err, &f) {
		return fmt.Sprintf("Could not check this key with %s. Check the endpoint address and try again.", name)
	}
	switch {
	case f.status == http.StatusUnauthorized || f.status == http.StatusForbidden:
		return fmt.Sprintf("%s rejected this API key. Check that you copied the whole key.", name)
	case f.transport:
		return fmt.Sprintf("Could not reach %s. Check the endpoint address and try again.", name)
	case f.status == http.StatusTooManyRequests:
		return fmt.Sprintf("%s is limiting requests for this key. Wait a minute and try again.", name)
	case f.status == http.StatusNotFound:
		return fmt.Sprintf("%s could not find this endpoint. Check the endpoint address.", name)
	case f.status >= 500:
		return fmt.Sprintf("%s is having a problem right now. Try again in a few minutes.", name)
	default:
		return fmt.Sprintf("%s did not accept this key (error %d). Check the key and the endpoint address.", name, f.status)
	}
}

// maxProviderBody caps a model-list response. OpenAI's full catalog is ~40KB;
// 4MB is headroom without letting a misconfigured endpoint stream forever.
const maxProviderBody = 4 << 20

// Azure OpenAI resource hostnames. A token whose base URL points at one of
// these is talking to Azure's OpenAI-compatible v1 surface rather than to
// OpenAI proper.
var azureHostSuffixes = []string{".openai.azure.com", ".services.ai.azure.com"}

// IsAzureEndpoint reports whether rawURL is an Azure OpenAI / Foundry resource.
// Matching is on the parsed HOST, not the raw string: a substring check would
// call `https://evil.com/?x=.openai.azure.com` an Azure endpoint and leak the
// resource key header to it.
func IsAzureEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range azureHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// NormalizeBaseURL canonicalises a user-pasted provider endpoint into the form
// the clients expect: an origin + optional path with NO trailing slash and no
// `/v1` suffix, because every caller appends its own versioned path
// (`/v1/chat/completions`, `/v1/messages`, `/v1/models`).
//
// This exists because the three things a user will realistically paste are all
// wrong by one path segment, and each fails as an opaque 404:
//
//   - Microsoft's docs show `https://<res>.openai.azure.com/openai/v1/` →
//     would build `/openai/v1/v1/chat/completions`.
//   - The Azure portal shows the bare `https://<res>.openai.azure.com/` →
//     would build `/v1/chat/completions`, missing Azure's `/openai` prefix.
//   - OpenAI's own docs show `https://api.openai.com/v1` → same doubling.
//
// Returns "" for an empty input (meaning "use the provider default").
func NormalizeBaseURL(provider, raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimRight(s, "/")
	// An extension builds its own URLs on the endpoint, so its path is kept.
	if _, isExt := extension.IDFromProvider(provider); isExt {
		return s
	}
	s = strings.TrimSuffix(s, "/v1")
	s = strings.TrimRight(s, "/")
	// An Azure resource root needs the `/openai` prefix that its v1 surface
	// lives under; OpenAI proper serves from the root, so only Azure gets this.
	if IsAzureEndpoint(s) {
		if u, err := url.Parse(s); err == nil && u.Path == "" {
			s += "/openai"
		}
	}
	return s
}

// ValidateBaseURL rejects endpoint overrides we can't safely call: the URL must
// parse, carry a host, and use TLS — an API key sent over plaintext http is a
// leaked key. Plain http to localhost is allowed in development only, for local
// gateways. Internal addresses are refused at dial time (netguard.Client), not
// here. Returns a client-safe message ("" = valid).
func ValidateBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "Base URL must be a full URL, e.g. https://your-resource.openai.azure.com/openai"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "Base URL must start with https://"
	}
	if u.Scheme == "http" && !(netguard.DevMode() && netguard.IsLocalhost(u.Hostname())) {
		return "Base URL must use https:// (an API key sent over http can be intercepted)"
	}
	if u.RawQuery != "" || u.Fragment != "" {
		// Azure's classic surface wants ?api-version=…; the v1 surface this
		// integration targets does not, and a query string here would be
		// silently dropped when the caller appends its own path.
		return "Base URL must not include a query string — the api-version parameter isn't used by the v1 API"
	}
	return ""
}

// SetOpenAIAuth applies key auth for an OpenAI-shaped endpoint. OpenAI proper
// takes a bearer token; Azure's v1 surface is documented BOTH ways — its SDK
// samples pass the resource key straight to the stock OpenAI client (which
// sends a bearer), while its REST samples use an `api-key` header. We can't
// tell from here which a given resource enforces, so Azure hosts get both and
// the service uses whichever it recognises.
//
// Used by the connection tester AND the translation call, deliberately: a
// token that tests "connected" under one auth shape and then translates under
// another would pass setup and fail in production.
func SetOpenAIAuth(req *http.Request, apiKey, base string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if IsAzureEndpoint(base) {
		req.Header.Set("api-key", apiKey)
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// DefaultTokenName is what a key with no name of its own is called. The first
// key on a vendor usually keeps it, since there is nothing yet to tell apart.
const DefaultTokenName = "Default"

// DisplayName is the vendor name as users see it — the default label for a new
// connection, and the vocabulary error copy uses.
func DisplayName(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderClaude:
		return "Claude"
	case ProviderDeepL:
		return "DeepL"
	case ProviderGoogle:
		return "Google Translate"
	default:
		return provider
	}
}
