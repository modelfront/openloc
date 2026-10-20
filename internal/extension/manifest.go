// Package extension installs and runs tenant extensions: zips holding a
// manifest and a WASM module (see pkg/extsdk). The WASM code runs in a
// sandbox with no network or file access of its own; it reaches a vendor API
// only through the host's http_request, which allows the manifest's hosts
// and fills in the API token.
package extension

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"openloc/pkg/extsdk"
)

// ProviderPrefix marks an AI vendor provider backed by an extension:
// "ext:<manifest id>".
const ProviderPrefix = "ext:"

// Provider is the AI vendor provider string for an extension id.
func Provider(id string) string { return ProviderPrefix + id }

// IDFromProvider returns the extension id of an "ext:" provider.
func IDFromProvider(provider string) (string, bool) {
	id, ok := strings.CutPrefix(provider, ProviderPrefix)
	return id, ok && id != ""
}

// BaseURLHost is the allowed_hosts entry that stands for the host of the
// key's connection URL.
const BaseURLHost = "{settings." + extsdk.SettingBaseURL + "}"

const (
	defaultBatchSize = 30
	maxBatchSize     = 100
	maxRPS           = 100
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	hostPattern = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]{1,5})?$`)
)

// ParseManifest decodes and validates manifest.json.
func ParseManifest(data []byte) (extsdk.Manifest, error) {
	var m extsdk.Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest.json is not valid: %w", err)
	}
	if err := Validate(&m); err != nil {
		return m, err
	}
	return m, nil
}

// Validate checks a manifest and fills defaults.
func Validate(m *extsdk.Manifest) error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id must be 1-40 lowercase letters, digits or dashes")
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len(m.Name) > 80 {
		return fmt.Errorf("name must be 1-80 characters")
	}
	if strings.TrimSpace(m.Version) == "" || len(m.Version) > 40 {
		return fmt.Errorf("version must be 1-40 characters")
	}
	if m.APIVersion != extsdk.APIVersion {
		return fmt.Errorf("api_version %d is not supported (this server supports %d)", m.APIVersion, extsdk.APIVersion)
	}
	if len(m.Description) > 500 {
		return fmt.Errorf("description must be at most 500 characters")
	}
	if err := validateCapabilities(m.Capabilities); err != nil {
		return err
	}
	if err := validateSettings(m.Settings); err != nil {
		return err
	}
	if err := validateHosts(m.AllowedHosts, hasSetting(*m, extsdk.SettingBaseURL)); err != nil {
		return err
	}
	if m.Limits.BatchSize == 0 {
		m.Limits.BatchSize = defaultBatchSize
	}
	if m.Limits.BatchSize < 1 || m.Limits.BatchSize > maxBatchSize {
		return fmt.Errorf("limits.batch_size must be between 1 and %d", maxBatchSize)
	}
	if m.Limits.RequestsPerSecond < 0 || m.Limits.RequestsPerSecond > maxRPS {
		return fmt.Errorf("limits.requests_per_second must be between 0 and %d", maxRPS)
	}
	if m.Entry == "" || strings.Contains(m.Entry, "/") || !strings.HasSuffix(m.Entry, ".wasm") {
		return fmt.Errorf("entry must name a .wasm file at the root of the zip")
	}
	if m.Icon != "" && (strings.Contains(m.Icon, "/") || !strings.HasSuffix(m.Icon, ".png")) {
		return fmt.Errorf("icon must name a .png file at the root of the zip")
	}
	return nil
}

func validateCapabilities(caps []string) error {
	if len(caps) == 0 {
		return fmt.Errorf("capabilities must list at least one of: qe, ape")
	}
	seen := map[string]bool{}
	for _, c := range caps {
		if c != extsdk.CapabilityQE && c != extsdk.CapabilityAPE {
			return fmt.Errorf("capability %q is not supported", c)
		}
		if seen[c] {
			return fmt.Errorf("capability %q is listed twice", c)
		}
		seen[c] = true
	}
	return nil
}

// validateSettings accepts the v1 settings: a secret token, and an optional
// base_url with a default.
func validateSettings(settings []extsdk.Setting) error {
	seen := map[string]bool{}
	for _, s := range settings {
		if seen[s.Key] {
			return fmt.Errorf("setting %q is listed twice", s.Key)
		}
		seen[s.Key] = true
		if strings.TrimSpace(s.Label) == "" {
			return fmt.Errorf("setting %q needs a label", s.Key)
		}
		switch s.Key {
		case extsdk.SettingToken:
			if !s.Secret {
				return fmt.Errorf("setting %q must be secret", s.Key)
			}
		case extsdk.SettingBaseURL:
			if s.Secret {
				return fmt.Errorf("setting %q cannot be secret", s.Key)
			}
			if s.Default != "" {
				if _, err := httpsURL(s.Default); err != nil {
					return fmt.Errorf("setting %q default: %w", s.Key, err)
				}
			}
		default:
			return fmt.Errorf("setting %q is not supported (use %q or %q)", s.Key, extsdk.SettingToken, extsdk.SettingBaseURL)
		}
	}
	if !seen[extsdk.SettingToken] {
		return fmt.Errorf("settings must include a secret %q", extsdk.SettingToken)
	}
	return nil
}

func validateHosts(hosts []string, hasBaseURL bool) error {
	for _, h := range hosts {
		if h == BaseURLHost {
			if !hasBaseURL {
				return fmt.Errorf("allowed_hosts uses %s but settings has no %q", BaseURLHost, extsdk.SettingBaseURL)
			}
			continue
		}
		if h != strings.ToLower(h) || !hostPattern.MatchString(h) || !strings.Contains(h, ".") {
			return fmt.Errorf("allowed host %q must be a lowercase host name such as api.example.com", h)
		}
	}
	return nil
}

func hasSetting(m extsdk.Manifest, key string) bool {
	for _, s := range m.Settings {
		if s.Key == key {
			return true
		}
	}
	return false
}

// DefaultBaseURL is the manifest's base_url default, or "".
func DefaultBaseURL(m extsdk.Manifest) string {
	for _, s := range m.Settings {
		if s.Key == extsdk.SettingBaseURL {
			return s.Default
		}
	}
	return ""
}

// HasCapability reports whether the manifest lists c.
func HasCapability(m extsdk.Manifest, c string) bool {
	for _, x := range m.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// httpsURL parses an absolute https URL.
func httpsURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("%q must be an https URL", raw)
	}
	return u, nil
}
