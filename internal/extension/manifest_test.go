package extension

import (
	"encoding/json"
	"strings"
	"testing"

	"openloc/pkg/extsdk"
)

// validManifest is a manifest Validate accepts; each case breaks one field.
func validManifest() extsdk.Manifest {
	return extsdk.Manifest{
		ID: "acme-qe", Name: "Acme QE", Version: "1.0.0", APIVersion: extsdk.APIVersion,
		Capabilities: []string{extsdk.CapabilityQE},
		Settings: []extsdk.Setting{
			{Key: extsdk.SettingToken, Label: "API token", Secret: true},
			{Key: extsdk.SettingBaseURL, Label: "Endpoint", Default: "https://api.example.com/v1"},
		},
		AllowedHosts: []string{"api.example.com", BaseURLHost},
		Entry:        "extension.wasm",
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*extsdk.Manifest)
		wantErr string // "" = valid
	}{
		{"valid", func(*extsdk.Manifest) {}, ""},
		{"host with port", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"api.example.com:8443"} }, ""},
		{"both capabilities", func(m *extsdk.Manifest) { m.Capabilities = []string{"qe", "ape"} }, ""},
		{"no base_url setting and no placeholder", func(m *extsdk.Manifest) {
			m.Settings = m.Settings[:1]
			m.AllowedHosts = []string{"api.example.com"}
		}, ""},

		{"uppercase id", func(m *extsdk.Manifest) { m.ID = "Acme" }, "id must be"},
		{"id with slash", func(m *extsdk.Manifest) { m.ID = "acme/qe" }, "id must be"},
		{"id starting with dash", func(m *extsdk.Manifest) { m.ID = "-acme" }, "id must be"},
		{"id too long", func(m *extsdk.Manifest) { m.ID = strings.Repeat("a", 41) }, "id must be"},
		{"empty id", func(m *extsdk.Manifest) { m.ID = "" }, "id must be"},
		{"blank name", func(m *extsdk.Manifest) { m.Name = "   " }, "name must be"},
		{"no version", func(m *extsdk.Manifest) { m.Version = "" }, "version must be"},
		{"future api version", func(m *extsdk.Manifest) { m.APIVersion = extsdk.APIVersion + 1 }, "is not supported"},

		{"no capabilities", func(m *extsdk.Manifest) { m.Capabilities = nil }, "at least one"},
		{"unsupported capability", func(m *extsdk.Manifest) { m.Capabilities = []string{"mt"} }, `capability "mt" is not supported`},
		{"duplicate capability", func(m *extsdk.Manifest) { m.Capabilities = []string{"qe", "qe"} }, "listed twice"},

		{"missing token setting", func(m *extsdk.Manifest) { m.Settings = m.Settings[1:] }, `secret "token"`},
		{"token not secret", func(m *extsdk.Manifest) { m.Settings[0].Secret = false }, "must be secret"},
		{"base_url secret", func(m *extsdk.Manifest) { m.Settings[1].Secret = true }, "cannot be secret"},
		{"base_url default not https", func(m *extsdk.Manifest) { m.Settings[1].Default = "http://api.example.com" }, "https URL"},
		{"unknown setting", func(m *extsdk.Manifest) {
			m.Settings = append(m.Settings, extsdk.Setting{Key: "region", Label: "Region"})
		}, `setting "region" is not supported`},
		{"setting without label", func(m *extsdk.Manifest) { m.Settings[0].Label = "" }, "needs a label"},

		{"base_url placeholder without base_url setting", func(m *extsdk.Manifest) { m.Settings = m.Settings[:1] }, "settings has no"},
		{"host with scheme", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"https://api.example.com"} }, "lowercase host name"},
		{"host with path", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"api.example.com/v1"} }, "lowercase host name"},
		{"uppercase host", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"API.example.com"} }, "lowercase host name"},
		{"single-label host", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"localhost"} }, "lowercase host name"},
		{"wildcard host", func(m *extsdk.Manifest) { m.AllowedHosts = []string{"*.example.com"} }, "lowercase host name"},

		{"batch size over the cap", func(m *extsdk.Manifest) { m.Limits.BatchSize = maxBatchSize + 1 }, "batch_size"},
		{"negative batch size", func(m *extsdk.Manifest) { m.Limits.BatchSize = -1 }, "batch_size"},
		{"negative rps", func(m *extsdk.Manifest) { m.Limits.RequestsPerSecond = -1 }, "requests_per_second"},
		{"rps over the cap", func(m *extsdk.Manifest) { m.Limits.RequestsPerSecond = maxRPS + 1 }, "requests_per_second"},

		{"entry in a folder", func(m *extsdk.Manifest) { m.Entry = "bin/extension.wasm" }, "entry must"},
		{"entry not wasm", func(m *extsdk.Manifest) { m.Entry = "extension.js" }, "entry must"},
		{"no entry", func(m *extsdk.Manifest) { m.Entry = "" }, "entry must"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(&m)
			err := Validate(&m)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// An unset batch size takes the default, and a set one is kept.
func TestValidate_BatchSizeDefault(t *testing.T) {
	m := validManifest()
	if err := Validate(&m); err != nil {
		t.Fatal(err)
	}
	if m.Limits.BatchSize != defaultBatchSize {
		t.Errorf("batch size = %d, want the default %d", m.Limits.BatchSize, defaultBatchSize)
	}
	m = validManifest()
	m.Limits.BatchSize = maxBatchSize
	if err := Validate(&m); err != nil || m.Limits.BatchSize != maxBatchSize {
		t.Errorf("batch size %d: err=%v, kept=%d", maxBatchSize, err, m.Limits.BatchSize)
	}
}

// Unknown manifest fields are refused, so a typo ("allowed_host") fails
// the upload instead of silently allowing nothing.
func TestParseManifest_RejectsUnknownFields(t *testing.T) {
	raw, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(raw); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	withTypo := strings.Replace(string(raw), `"allowed_hosts"`, `"allowed_host"`, 1)
	if _, err := ParseManifest([]byte(withTypo)); err == nil {
		t.Fatal("a manifest with an unknown field was accepted")
	}
	if _, err := ParseManifest([]byte("{not json")); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestProviderRoundTrip(t *testing.T) {
	if p := Provider("acme-qe"); p != "ext:acme-qe" {
		t.Fatalf("Provider = %q", p)
	}
	if id, ok := IDFromProvider("ext:acme-qe"); !ok || id != "acme-qe" {
		t.Errorf("IDFromProvider(ext:acme-qe) = %q, %v", id, ok)
	}
	for _, p := range []string{"openai", "ext:", "", "extension:acme"} {
		if _, ok := IDFromProvider(p); ok {
			t.Errorf("IDFromProvider(%q) accepted a non-extension provider", p)
		}
	}
}
