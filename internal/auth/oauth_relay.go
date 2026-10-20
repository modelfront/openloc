package auth

import (
	"net/url"
	"regexp"
	"strings"
)

// Google and Microsoft accept only redirect URIs that were registered
// beforehand, character for character, and neither supports wildcards. Every
// staging branch gets its own hostname, so a per-branch redirect URI means a
// console edit per branch. Instead all deployments share ONE registered URI —
// production's — and the branch that started the flow is carried in the OAuth
// `state`. Production forwards the authorization code to that branch, which
// exchanges it against its own database with its own JWT_SECRET.
//
// The `state` is stamped as `h=<host>|<state>`. The provider echoes it
// verbatim, and the callback still compares the whole string to the cookie it
// set, so CSRF protection is untouched: the cookie lives on the branch host,
// and a code forwarded anywhere else has no cookie to match.
const (
	stateHostPrefix = "h="
	stateHostSep    = "|"
)

// A forward target must be a single DNS label under the configured domain.
// Labels are lowercase because a Kubernetes Ingress host cannot be anything
// else — so an uppercase label is never one of ours.
var relayLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// OAuthRelay is one deployment's view of the shared redirect URI.
type OAuthRelay struct {
	// SelfBaseURL is BASE_URL — the origin this deployment actually serves.
	SelfBaseURL string
	// RedirectBaseURL is the origin registered with the providers. Equal to
	// SelfBaseURL on the deployment that owns the registration.
	RedirectBaseURL string
	// Domain is the parent domain this deployment may forward callbacks to.
	// Empty refuses every forward.
	Domain string
}

// NewOAuthRelay normalizes the pair: an empty redirectBaseURL means this
// deployment owns its own redirect URI, which is the case for production and
// for local dev.
func NewOAuthRelay(selfBaseURL, redirectBaseURL, domain string) OAuthRelay {
	if redirectBaseURL == "" {
		redirectBaseURL = selfBaseURL
	}
	return OAuthRelay{
		SelfBaseURL:     selfBaseURL,
		RedirectBaseURL: redirectBaseURL,
		Domain:          strings.ToLower(strings.TrimSpace(domain)),
	}
}

// stamp reports whether sign-in must record this deployment's host in the
// state — true only when the redirect URI belongs to a different host, so
// production and local dev keep the plain state format.
func (r OAuthRelay) stamp() bool {
	self := r.selfHost()
	return self != "" && self != originHost(r.RedirectBaseURL)
}

// selfHost is the host this deployment serves.
func (r OAuthRelay) selfHost() string {
	return originHost(r.SelfBaseURL)
}

// forwardTarget resolves the absolute URL a relayed callback goes to, or
// refuses. Only `<label>.<Domain>` passes, so the target can only ever be one
// of our own hosts — this must stay strict or the endpoint becomes an open
// redirector for authorization codes.
func (r OAuthRelay) forwardTarget(host, callbackPath string) (string, bool) {
	if r.Domain == "" || host == "" || host == r.selfHost() {
		return "", false
	}
	suffix := "." + r.Domain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	if !relayLabel.MatchString(strings.TrimSuffix(host, suffix)) {
		return "", false
	}
	return "https://" + host + callbackPath, true
}

// relayedQuery rebuilds the query string for a forwarded callback from the
// OAuth parameters alone. Passing the provider's raw query through would let a
// crafted authorization URL append arbitrary parameters to one of our hosts.
func relayedQuery(q url.Values) url.Values {
	out := url.Values{}
	for _, key := range []string{"code", "state", "error", "error_description"} {
		if v := q.Get(key); v != "" {
			out.Set(key, v)
		}
	}
	return out
}

// stampStateHost records the host that started the flow in front of the state.
func stampStateHost(state, host string) string {
	return stateHostPrefix + host + stateHostSep + state
}

// splitStateHost peels the host off a stamped state. An unstamped state comes
// back unchanged with an empty host, which is what production and local dev
// produce.
func splitStateHost(state string) (host, rest string) {
	if !strings.HasPrefix(state, stateHostPrefix) {
		return "", state
	}
	trimmed := state[len(stateHostPrefix):]
	sep := strings.Index(trimmed, stateHostSep)
	if sep < 0 {
		return "", state
	}
	return trimmed[:sep], trimmed[sep+len(stateHostSep):]
}

// originHost extracts the host (with port, if any) from a base URL. Returns ""
// for anything unparseable, which makes stamp() and forwardTarget() refuse
// rather than guess.
func originHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return u.Host
}
