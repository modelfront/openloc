package auth

import "testing"

const relayDomain = "app.openloc.com"

const (
	prodBase   = "https://app.openloc.com"
	branchBase = "https://product-docs.app.openloc.com"
)

func TestOAuthRelay_Stamp(t *testing.T) {
	tests := []struct {
		name     string
		self     string
		redirect string
		want     bool
	}{
		{"deployment owns its redirect URI", prodBase, prodBase, false},
		{"empty redirect base falls back to self", prodBase, "", false},
		{"branch borrows the production redirect URI", branchBase, prodBase, true},
		{"local dev", "http://localhost:5173", "", false},
		{"same host, different scheme", "http://localhost:5173", "https://localhost:5173", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewOAuthRelay(tt.self, tt.redirect, relayDomain).stamp()
			if got != tt.want {
				t.Errorf("stamp() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOAuthRelay_ForwardTarget(t *testing.T) {
	const path = "/api/auth/google/callback"

	tests := []struct {
		name   string
		domain string
		host   string
		want   string
	}{
		{"branch host", relayDomain, "product-docs.app.openloc.com", "https://product-docs.app.openloc.com" + path},
		{"single-char label", relayDomain, "a.app.openloc.com", "https://a.app.openloc.com" + path},
		{"relaying disabled", "", "product-docs.app.openloc.com", ""},
		{"own host", relayDomain, "app.openloc.com", ""},
		{"empty host", relayDomain, "", ""},
		{"nested label", relayDomain, "a.b.app.openloc.com", ""},
		{"suffix spoof", relayDomain, "xapp.openloc.com", ""},
		{"longer suffix spoof", relayDomain, "evilapp.openloc.com", ""},
		{"foreign domain", relayDomain, "evil.com", ""},
		{"foreign domain ending in ours", relayDomain, "evil.com.app.openloc.com.evil.com", ""},
		{"uppercase label", relayDomain, "Foo.app.openloc.com", ""},
		{"underscore label", relayDomain, "a_b.app.openloc.com", ""},
		{"label with port", relayDomain, "foo.app.openloc.com:8080", ""},
		{"trailing hyphen", relayDomain, "foo-.app.openloc.com", ""},
		{"leading hyphen", relayDomain, "-foo.app.openloc.com", ""},
		{"embedded credentials", relayDomain, "evil.com@foo.app.openloc.com", ""},
		{"path injection", relayDomain, "evil.com/x.app.openloc.com", ""},
		{"query injection", relayDomain, "evil.com?.app.openloc.com", ""},
		{"fragment injection", relayDomain, "evil.com#.app.openloc.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relay := NewOAuthRelay(prodBase, prodBase, tt.domain)
			got, ok := relay.forwardTarget(tt.host, path)
			if tt.want == "" {
				if ok {
					t.Errorf("forwardTarget(%q) = %q, want refusal", tt.host, got)
				}
				return
			}
			if !ok {
				t.Fatalf("forwardTarget(%q) refused, want %q", tt.host, tt.want)
			}
			if got != tt.want {
				t.Errorf("forwardTarget(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestStateHostRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		state string
		host  string
	}{
		{"plain nonce", "nonce123", "product-docs.app.openloc.com"},
		{"nonce with invite token", "nonce123:TOKEN-_abc", "product-docs.app.openloc.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stamped := stampStateHost(tt.state, tt.host)
			host, rest := splitStateHost(stamped)
			if host != tt.host {
				t.Errorf("host = %q, want %q", host, tt.host)
			}
			if rest != tt.state {
				t.Errorf("rest = %q, want %q", rest, tt.state)
			}
		})
	}
}

func TestSplitStateHost_Unstamped(t *testing.T) {
	tests := []struct {
		name  string
		state string
	}{
		{"no prefix", "nonce123"},
		{"no prefix, with invite", "nonce123:TOKEN"},
		{"prefix without separator", "h=product-docs.app.openloc.com"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, rest := splitStateHost(tt.state)
			if host != "" {
				t.Errorf("host = %q, want empty", host)
			}
			if rest != tt.state {
				t.Errorf("rest = %q, want %q", rest, tt.state)
			}
		})
	}
}
