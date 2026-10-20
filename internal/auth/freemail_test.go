package auth

import (
	"strings"
	"testing"
)

func TestIsFreeEmailDomain(t *testing.T) {
	cases := map[string]bool{
		"gmail.com":      true,
		"GMAIL.COM":      true, // case-insensitive
		"@outlook.com":   true, // leading @ tolerated
		"hotmail.com":    true,
		"proton.me":      true,
		"example.com":    false,
		"example.org":    false,
		"example.net":    false,
		"sub.acme.co.uk": false,
	}
	for domain, want := range cases {
		if got := IsFreeEmailDomain(domain); got != want {
			t.Errorf("IsFreeEmailDomain(%q) = %v, want %v", domain, got, want)
		}
	}
}

func TestEmailDomain(t *testing.T) {
	cases := map[string]string{
		"john@example.com":  "example.com",
		"JOHN@Example.COM":  "example.com",
		"  a@b.io  ":        "b.io",
		"nobody":            "",
		"@nope.com":         "nope.com", // empty local part still yields a domain
		"trailing@":         "",
	}
	for email, want := range cases {
		if got := emailDomain(email); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestOrgNameFromDomain(t *testing.T) {
	cases := map[string]string{
		"example.com":    "Example",
		"example.org":    "Example",
		"sub.acme.co.uk": "Sub",
		"":               "Organization",
	}
	for domain, want := range cases {
		if got := orgNameFromDomain(domain); got != want {
			t.Errorf("orgNameFromDomain(%q) = %q, want %q", domain, got, want)
		}
	}
}

func TestMicrosoftOAuthConfig(t *testing.T) {
	// Unset client ID → nil (disables the routes, same as Google).
	if cfg := MicrosoftOAuthConfig("", "secret", "http://x"); cfg != nil {
		t.Fatal("expected nil config when client ID is empty")
	}
	cfg := MicrosoftOAuthConfig("client-id", "secret", "http://localhost:5173")
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.RedirectURL != "http://localhost:5173/api/auth/microsoft/callback" {
		t.Errorf("unexpected redirect URL: %s", cfg.RedirectURL)
	}
	// Work/school-only authority — this is what blocks personal accounts.
	if !strings.Contains(cfg.Endpoint.AuthURL, "/organizations/") {
		t.Errorf("expected 'organizations' authority in auth URL, got %s", cfg.Endpoint.AuthURL)
	}
}
