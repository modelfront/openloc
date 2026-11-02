package qa

import (
	"bytes"
	"encoding/json"
	"fmt"

	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/qa/numeric"
)

// MaxNumberFormatLanguages caps the languages one number_format rule names.
const MaxNumberFormatLanguages = 100

var (
	numberDecimals = map[string]rune{".": '.', ",": ','}
	numberGroups   = map[string]rune{",": ',', ".": '.', " ": ' ', "'": '\'', "": 0}
)

// ValidateNumberFormatConfig checks a number_format config and returns it with
// its mode filled in and its language codes normalized, ready to store. Every
// problem is an httputil.ClientError.
func ValidateNumberFormatConfig(raw []byte) ([]byte, error) {
	cfg, err := parseNumberFormatConfig(raw, true)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}

// ParseNumberFormatConfig reads a stored number_format config. A config the
// API would refuse comes back as the default, with the error.
func ParseNumberFormatConfig(raw []byte) (NumberFormatConfig, error) {
	cfg, err := parseNumberFormatConfig(raw, false)
	if err != nil {
		return NumberFormatConfig{Mode: "allow"}, err
	}
	return cfg, nil
}

func parseNumberFormatConfig(raw []byte, strict bool) (NumberFormatConfig, error) {
	var cfg NumberFormatConfig
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if strict {
			dec.DisallowUnknownFields()
		}
		if err := dec.Decode(&cfg); err != nil {
			return cfg, httputil.NewClientError("number_format config: " + err.Error())
		}
	}
	switch cfg.Mode {
	case "":
		cfg.Mode = "allow"
	case "allow", "require":
	default:
		return cfg, httputil.NewClientError(fmt.Sprintf("number_format mode must be allow or require, not %q", cfg.Mode))
	}
	if len(cfg.Languages) > MaxNumberFormatLanguages {
		return cfg, httputil.NewClientError(fmt.Sprintf("number_format languages: at most %d", MaxNumberFormatLanguages))
	}
	cfg.separators = make(map[string]numeric.Separators, len(cfg.Languages))
	for i, l := range cfg.Languages {
		code := langcode.Normalize(l.Language)
		if code == "" {
			return cfg, httputil.NewClientError("number_format languages: empty language code")
		}
		if _, dup := cfg.separators[code]; dup {
			return cfg, httputil.NewClientError(fmt.Sprintf("number_format languages: %s is listed twice", code))
		}
		d, ok := numberDecimals[l.Decimal]
		if !ok {
			return cfg, httputil.NewClientError(fmt.Sprintf(`number_format languages: %s decimal must be "." or ","`, code))
		}
		g, ok := numberGroups[l.Group]
		if !ok {
			return cfg, httputil.NewClientError(fmt.Sprintf(`number_format languages: %s group must be ",", ".", " ", "'" or empty`, code))
		}
		if d == g {
			return cfg, httputil.NewClientError(fmt.Sprintf("number_format languages: %s decimal and group must differ", code))
		}
		cfg.Languages[i].Language = code
		cfg.separators[code] = numeric.Separators{Decimal: d, Group: g}
	}
	return cfg, nil
}

// SeparatorsFor returns the separators the config sets for a language: its
// locale's own entry, else its bare language's, else none.
func (c NumberFormatConfig) SeparatorsFor(code string) numeric.Separators {
	code = langcode.Normalize(code)
	if s, ok := c.separators[code]; ok {
		return s
	}
	return c.separators[langcode.BaseLang(code)]
}

// checkNumberGroup runs the enabled rules of the Numbers group together:
// number_consistency, number_format, date_consistency, time_consistency and
// unit_conversion.
func checkNumberGroup(p CheckAllParams) []Issue {
	r := numeric.Rules{
		Numbers: p.Rules["number_consistency"],
		Format:  p.Rules["number_format"],
		Dates:   p.Rules["date_consistency"],
		Times:   p.Rules["time_consistency"],
		Units:   p.Rules["unit_conversion"],
	}
	// The rule's mode and language separators count only while it is on.
	if r.Format != "" {
		r.FormatRequire = p.NumberFormat.Mode == "require"
		r.SourceSeparators = p.NumberFormat.SeparatorsFor(p.SourceLanguage)
		r.TargetSeparators = p.NumberFormat.SeparatorsFor(p.TargetLanguage)
	}
	return issuesOf(numeric.Run(p.Source, p.Target, p.SourceLanguage, p.TargetLanguage, r))
}

func issuesOf(findings []numeric.Finding) []Issue {
	if len(findings) == 0 {
		return nil
	}
	out := make([]Issue, len(findings))
	for i, f := range findings {
		out[i] = Issue{CheckType: f.CheckType, Severity: f.Severity, Message: f.Message}
	}
	return out
}
