package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/inlinetag"
	"openloc/internal/langcode"
)

// RegexCheckType is the rule type whose logic comes entirely from its config.
// A profile may hold many of them; each finding is stored under
// RegexFindingCheckType(rule id), so findings of two rules never collide.
const RegexCheckType = "regex"

// Regex rule modes.
const (
	RegexModeTargetMustNotMatch   = "target_must_not_match"
	RegexModeTargetMustMatch      = "target_must_match"
	RegexModeSourceMatchTargetNot = "source_match_target_not"
	RegexModeSourceMustNotMatch   = "source_must_not_match"
)

// RegexModes lists the valid modes, for validation and tool schemas.
var RegexModes = []string{
	RegexModeTargetMustNotMatch,
	RegexModeTargetMustMatch,
	RegexModeSourceMatchTargetNot,
	RegexModeSourceMustNotMatch,
}

// Limits on a regex rule.
const (
	MaxRegexRulesPerProfile = 50
	maxRegexPatternLen      = 1000
	maxRegexNameLen         = 100
	maxRegexMessageLen      = 300
)

// regexTestTimeout bounds the save-time test run of a pattern on
// regexTestSample. Go's regexp runs in linear time, so a pattern that misses
// this budget on 100 KB would slow every QA run noticeably.
var regexTestTimeout = time.Second

// regexTestSample is ~100 KB of mixed text: letters, digits, units, regular,
// non-breaking and narrow non-breaking spaces, punctuation and a tag.
var regexTestSample = strings.Repeat("Lorem 12 kg ipsum 5\u00a0cm, dolor 3\u202f% <b>sit</b> amet: «x» 1 000,50 €. ", 1200)

// RegexFindingCheckType is the check_type of the findings a regex rule writes.
func RegexFindingCheckType(ruleID int64) string {
	return RegexCheckType + ":" + strconv.FormatInt(ruleID, 10)
}

// regexRule is a compiled, enabled regex rule.
type regexRule struct {
	checkType string
	severity  string
	mode      string
	message   string
	pattern   *regexp.Regexp
	source    *regexp.Regexp
	languages []string
}

// ValidateRegexRuleConfig parses and checks a regex rule's config and returns
// it with its language codes normalized, ready to store. Every problem is an
// httputil.ClientError naming the field.
func ValidateRegexRuleConfig(ctx context.Context, raw []byte) ([]byte, error) {
	cfg, err := parseRegexConfig(raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, httputil.NewClientError("name is required")
	}
	if utf8.RuneCountInString(cfg.Name) > maxRegexNameLen {
		return nil, httputil.NewClientError(fmt.Sprintf("name: at most %d characters", maxRegexNameLen))
	}
	if !validRegexMode(cfg.Mode) {
		return nil, httputil.NewClientError("mode must be one of " + strings.Join(RegexModes, ", "))
	}
	if strings.TrimSpace(cfg.Message) == "" {
		return nil, httputil.NewClientError("message is required")
	}
	if utf8.RuneCountInString(cfg.Message) > maxRegexMessageLen {
		return nil, httputil.NewClientError(fmt.Sprintf("message: at most %d characters", maxRegexMessageLen))
	}
	if err := checkPattern(ctx, "pattern", cfg.Pattern, cfg.IgnoreCase); err != nil {
		return nil, err
	}
	if cfg.Mode == RegexModeSourceMatchTargetNot {
		if err := checkPattern(ctx, "source_pattern", cfg.SourcePattern, cfg.IgnoreCase); err != nil {
			return nil, err
		}
	} else if cfg.SourcePattern != "" {
		return nil, httputil.NewClientError("source_pattern is used only in mode " + RegexModeSourceMatchTargetNot)
	}
	langs := make([]string, 0, len(cfg.TargetLanguages))
	seen := make(map[string]bool, len(cfg.TargetLanguages))
	for _, l := range cfg.TargetLanguages {
		n := langcode.Normalize(l)
		if n == "" {
			return nil, httputil.NewClientError("target_languages: empty language code")
		}
		if !seen[n] {
			seen[n] = true
			langs = append(langs, n)
		}
	}
	cfg.TargetLanguages = langs
	return marshalRegexConfig(cfg)
}

// marshalRegexConfig encodes a config without HTML escaping, so <, > and &
// in a pattern are stored as typed.
func marshalRegexConfig(cfg RegexRuleConfig) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func parseRegexConfig(raw []byte) (RegexRuleConfig, error) {
	var cfg RegexRuleConfig
	if len(bytes.TrimSpace(raw)) == 0 {
		return cfg, httputil.NewClientError("config is required for a regex rule")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, httputil.NewClientError("config: " + err.Error())
	}
	return cfg, nil
}

func validRegexMode(mode string) bool {
	for _, m := range RegexModes {
		if m == mode {
			return true
		}
	}
	return false
}

func compileRegex(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

// checkPattern rejects a pattern that is empty, too long, does not compile,
// matches empty text (it would fire on every segment, or never), or takes
// longer than regexTestTimeout on regexTestSample.
func checkPattern(ctx context.Context, field, pattern string, ignoreCase bool) error {
	if pattern == "" {
		return httputil.NewClientError(field + " is required")
	}
	if utf8.RuneCountInString(pattern) > maxRegexPatternLen {
		return httputil.NewClientError(fmt.Sprintf("%s: at most %d characters", field, maxRegexPatternLen))
	}
	re, err := compileRegex(pattern, ignoreCase)
	if err != nil {
		return httputil.NewClientError(field + ": " + strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	}
	if re.MatchString("") {
		return httputil.NewClientError(field + " matches empty text; it must match at least one character")
	}
	ctx, cancel := context.WithTimeout(ctx, regexTestTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		re.FindAllStringIndex(regexTestSample, -1)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return httputil.NewClientError(field + " is too slow; simplify it")
	}
}

// compileRegexRule turns a stored rule into a regexRule. It fails only for a
// row whose config no longer validates.
func compileRegexRule(rule db.QaRule) (regexRule, error) {
	cfg, err := parseRegexConfig(rule.Config)
	if err != nil {
		return regexRule{}, err
	}
	if !validRegexMode(cfg.Mode) {
		return regexRule{}, fmt.Errorf("invalid mode %q", cfg.Mode)
	}
	out := regexRule{
		checkType: RegexFindingCheckType(rule.ID),
		severity:  rule.Severity,
		mode:      cfg.Mode,
		message:   cfg.Message,
		languages: langcode.NormalizeAll(cfg.TargetLanguages),
	}
	if out.pattern, err = compileRegex(cfg.Pattern, cfg.IgnoreCase); err != nil {
		return regexRule{}, err
	}
	if cfg.Mode == RegexModeSourceMatchTargetNot {
		if out.source, err = compileRegex(cfg.SourcePattern, cfg.IgnoreCase); err != nil {
			return regexRule{}, err
		}
	}
	return out, nil
}

// regexTexts returns a segment's source and target as the editor shows them,
// which is what a regex rule matches: inline tags as their labels (<1>, </1>,
// {1}), shared by both sides, and escaped text decoded. A side without rich
// text is its plain text.
func regexTexts(source, target, sourceRich, targetRich string) (string, string) {
	t := inlinetag.NewTable()
	if sourceRich != "" {
		source = inlinetag.EncodeVisible(t, sourceRich)
	}
	if targetRich != "" {
		target = inlinetag.EncodeVisible(t, targetRich)
	}
	return source, target
}

// appliesTo reports whether the rule runs for a target language. An empty
// list means every language; a bare language ("fr") covers all its regional
// variants, a full locale ("fr-ca") only itself.
func (r regexRule) appliesTo(targetLanguage string) bool {
	if len(r.languages) == 0 {
		return true
	}
	tl := langcode.Normalize(targetLanguage)
	for _, l := range r.languages {
		if l == tl || (!strings.Contains(l, "-") && l == langcode.BaseLang(tl)) {
			return true
		}
	}
	return false
}

// check returns the rule's finding for one segment, or nil. A blank target is
// left to the empty_target check, except in source_must_not_match mode, which
// reads only the source.
func (r regexRule) check(source, target string) *Issue {
	blank := strings.TrimSpace(target) == ""
	var fire bool
	switch r.mode {
	case RegexModeTargetMustNotMatch:
		fire = !blank && r.pattern.MatchString(target)
	case RegexModeTargetMustMatch:
		fire = !blank && !r.pattern.MatchString(target)
	case RegexModeSourceMatchTargetNot:
		fire = !blank && r.source.MatchString(source) && !r.pattern.MatchString(target)
	case RegexModeSourceMustNotMatch:
		fire = r.pattern.MatchString(source)
	}
	if !fire {
		return nil
	}
	return &Issue{CheckType: r.checkType, Severity: r.severity, Message: r.message}
}
