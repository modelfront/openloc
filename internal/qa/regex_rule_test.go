package qa

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

func mustRegexRule(t *testing.T, cfg RegexRuleConfig) regexRule {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ValidateRegexRuleConfig(context.Background(), raw)
	if err != nil {
		t.Fatalf("validate %+v: %v", cfg, err)
	}
	rr, err := compileRegexRule(db.QaRule{ID: 7, CheckType: RegexCheckType, Severity: "error", Config: stored})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return rr
}

func TestRegexRule_Modes(t *testing.T) {
	unit := `\d (kg|cm)`
	cases := []struct {
		name           string
		cfg            RegexRuleConfig
		source, target string
		fire           bool
	}{
		{"must not match: regular space fires", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: unit}, "5 kg", "5 kg", true},
		{"must not match: nbsp passes", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: unit}, "5 kg", "5\u00a0kg", false},
		{"must not match: narrow nbsp passes", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: unit}, "5 kg", "5\u202fkg", false},
		{"must not match: blank target is left to empty_target", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: unit}, "5 kg", "  ", false},
		{"must match: missing fires", RegexRuleConfig{Mode: RegexModeTargetMustMatch, Pattern: `\.$`}, "Done.", "Fertig", true},
		{"must match: present passes", RegexRuleConfig{Mode: RegexModeTargetMustMatch, Pattern: `\.$`}, "Done.", "Fertig.", false},
		{"must match: blank target is left to empty_target", RegexRuleConfig{Mode: RegexModeTargetMustMatch, Pattern: `\.$`}, "Done.", "", false},
		{"source+target: source hit, target miss fires", RegexRuleConfig{Mode: RegexModeSourceMatchTargetNot, SourcePattern: `\bACME\b`, Pattern: `\bACME\b`}, "Ask ACME", "Fragen Sie Acme", true},
		{"source+target: both hit passes", RegexRuleConfig{Mode: RegexModeSourceMatchTargetNot, SourcePattern: `\bACME\b`, Pattern: `\bACME\b`}, "Ask ACME", "Fragen Sie ACME", false},
		{"source+target: source miss passes", RegexRuleConfig{Mode: RegexModeSourceMatchTargetNot, SourcePattern: `\bACME\b`, Pattern: `\bACME\b`}, "Ask us", "Fragen Sie uns", false},
		{"source must not match: fires on blank target too", RegexRuleConfig{Mode: RegexModeSourceMustNotMatch, Pattern: `TODO`}, "TODO fix", "", true},
		{"source must not match: target ignored", RegexRuleConfig{Mode: RegexModeSourceMustNotMatch, Pattern: `TODO`}, "fine", "TODO", false},
		{"ignore case", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `colour`, IgnoreCase: true}, "x", "COLOUR", true},
		{"case sensitive by default", RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `colour`}, "x", "COLOUR", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.cfg.Name, c.cfg.Message = "rule", "the rule's message"
			issue := mustRegexRule(t, c.cfg).check(c.source, c.target)
			if (issue != nil) != c.fire {
				t.Fatalf("fire = %v, want %v", issue != nil, c.fire)
			}
			if issue != nil && (issue.CheckType != "regex:7" || issue.Message != "the rule's message" || issue.Severity != "error") {
				t.Errorf("issue = %+v", issue)
			}
		})
	}
}

func TestRegexRule_AppliesTo(t *testing.T) {
	cases := []struct {
		langs []string
		lang  string
		want  bool
	}{
		{nil, "de-DE", true},
		{[]string{"fr"}, "fr-FR", true},
		{[]string{"fr"}, "fr_CA", true},
		{[]string{"fr"}, "fr", true},
		{[]string{"fr-CA"}, "fr-ca", true},
		{[]string{"fr-CA"}, "fr-FR", false},
		{[]string{"fr-CA"}, "fr", false},
		{[]string{"de", "pt-BR"}, "pt-br", true},
		{[]string{"de", "pt-BR"}, "pt-PT", false},
		{[]string{"fr"}, "frr", false},
	}
	for _, c := range cases {
		rr := mustRegexRule(t, RegexRuleConfig{Name: "n", Message: "m", Mode: RegexModeTargetMustMatch, Pattern: "x", TargetLanguages: c.langs})
		if got := rr.appliesTo(c.lang); got != c.want {
			t.Errorf("languages %v, target %q: got %v, want %v", c.langs, c.lang, got, c.want)
		}
	}
}

func TestValidateRegexRuleConfig_KeepsTextExactly(t *testing.T) {
	in := `{"name":" Espace ","mode":"target_must_not_match","pattern":"\\d[ \u00a0]<b>&","message":"Fine\u202fspace ","target_languages":["FR_ca","fr-CA"]}`
	out, err := ValidateRegexRuleConfig(context.Background(), []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var cfg RegexRuleConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Pattern != "\\d[ \u00a0]<b>&" || cfg.Message != "Fine\u202fspace " || cfg.Name != " Espace " {
		t.Errorf("text changed: %+v", cfg)
	}
	if strings.Contains(string(out), `\u003c`) {
		t.Errorf("stored config is HTML-escaped: %s", out)
	}
	if len(cfg.TargetLanguages) != 1 || cfg.TargetLanguages[0] != "fr-ca" {
		t.Errorf("languages not normalized and deduplicated: %v", cfg.TargetLanguages)
	}
}

func TestValidateRegexRuleConfig_SlowPatternRejected(t *testing.T) {
	old := regexTestTimeout
	regexTestTimeout = time.Nanosecond
	defer func() { regexTestTimeout = old }()

	raw := []byte(`{"name":"n","mode":"target_must_not_match","pattern":"(\\w+\\s?)+x","message":"m"}`)
	_, err := ValidateRegexRuleConfig(context.Background(), raw)
	msg, ok := httputil.AsClientError(err)
	if !ok || !strings.Contains(msg, "too slow") {
		t.Fatalf("want a too-slow client error, got %v", err)
	}
}

// The segment as the editor shows it: inline tags as their labels, shared by
// source and target, and escaped text decoded.
func TestCheckSegment_RegexSeesTagLabels(t *testing.T) {
	bold := func(before, word string) string {
		return before + `<bpt id="1">&lt;b&gt;</bpt>` + word + `<ept id="1">&lt;/b&gt;</ept>`
	}
	cases := []struct {
		name                   string
		cfg                    RegexRuleConfig
		source, target         string
		sourceRich, targetRich string
		fire                   bool
	}{
		{
			"paired tag labels are visible",
			RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `\d <1>kg</1>`},
			"Weighs 5 kg", "Pèse 5 kg", bold("Weighs 5 ", "kg"), bold("Pèse 5 ", "kg"), true,
		},
		{
			"a standalone tag is a {n} label",
			RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `\{1\}$`},
			"Press", "Appuyez", `Press <x id="1"/>`, `Appuyez <x id="1"/>`, true,
		},
		{
			"a placeholder shows its content",
			RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: ` <br/>$`},
			"Press", "Appuyez", `Press <ph id="1">&lt;br/&gt;</ph>`, `Appuyez <ph id="1">&lt;br/&gt;</ph>`, true,
		},
		{
			"source and target share labels",
			RegexRuleConfig{Mode: RegexModeSourceMatchTargetNot, SourcePattern: `<1>`, Pattern: `<1>`},
			"Click here", "Cliquez ici", bold("Click ", "here"), "Cliquez ici", true,
		},
		{
			"source and target share labels: tag kept",
			RegexRuleConfig{Mode: RegexModeSourceMatchTargetNot, SourcePattern: `<1>`, Pattern: `<1>`},
			"Click here", "Cliquez ici", bold("Click ", "here"), bold("Cliquez ", "ici"), false,
		},
		{
			"escaped text is decoded",
			RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `Tom & Jerry <1>`},
			"Tom & Jerry", "Tom & Jerry", bold("Tom &amp; Jerry ", "x"), bold("Tom &amp; Jerry ", "x"), true,
		},
		{
			"plain text without rich text is used as is",
			RegexRuleConfig{Mode: RegexModeTargetMustNotMatch, Pattern: `\d kg`},
			"5 kg", "5 kg", "", "", true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.cfg.Name, c.cfg.Message = "rule", "message"
			rs := ruleSet{regex: []regexRule{mustRegexRule(t, c.cfg)}}
			issues := (&Service{}).checkSegment(context.Background(), rs, segmentScope{}, batchLookups{}, 1, c.source, c.target, c.sourceRich, c.targetRich)
			if (len(issues) == 1) != c.fire || len(issues) > 1 {
				t.Fatalf("issues = %+v, want fire=%v", issues, c.fire)
			}
		})
	}
}

// A batch with more regex rules than a profile may hold is refused before any
// pattern is compiled or test-run, so its size bounds the work.
func TestRulesParams_RegexCapBeforePatternTests(t *testing.T) {
	in := make([]RuleInput, MaxRegexRulesPerProfile+1)
	for i := range in {
		in[i] = RuleInput{CheckType: RegexCheckType, Config: []byte(`{"name":"n","mode":"target_must_match","pattern":"(","message":"m"}`)}
	}
	_, err := rulesParams(context.Background(), 1, in)
	msg, ok := httputil.AsClientError(err)
	if !ok || !strings.Contains(msg, "at most 50 regex rules") {
		t.Fatalf("want the cap refusal before any pattern error, got %v", err)
	}
}
