package qa

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"openloc/internal/httputil"
)

func runPlaceables(source, target, config string) []string {
	p := CheckAllParams{
		Source: source, Target: target,
		SourceLanguage: "en", TargetLanguage: "de",
		Rules: map[string]string{PlaceablesCheckType: "warning"},
	}
	if config != "" {
		p.RuleConfigs = map[string][]byte{PlaceablesCheckType: []byte(config)}
	}
	var out []string
	for _, i := range CheckAllWithParams(context.Background(), p) {
		if i.CheckType == PlaceablesCheckType {
			out = append(out, i.Message)
		}
	}
	return out
}

func TestCheckPlaceables(t *testing.T) {
	cases := []struct {
		name, source, target string
		want                 []string
	}{
		{"all carried over", "Hello {name}, see https://example.com/help or mail test@test.com about XJ-500B.",
			"Hallo {name}, siehe https://example.com/help oder test@test.com zu XJ-500B.", nil},
		{"missing variable", "Hello {name}, you have %d items", "Hallo, du hast %d Artikel",
			[]string{"Placeable(s) missing from target: {name}"}},
		{"changed variable is missing and extra, in one finding", "Hello {name}", "Hallo {nmae}",
			[]string{"Placeable(s) missing from target: {name}; not in source: {nmae}"}},
		{"repeated variable counted", "{0} and {0}", "{0} und", []string{"Placeable(s) missing from target: {0}"}},
		{"missing code and url", "Order XJ-500B at www.example.com", "Bestellen Sie es",
			[]string{"Placeable(s) missing from target: XJ-500B, www.example.com"}},
		{"numbers are not compared", "Costs 1,234.50 on 2024-03-14", "Kostet 1.234,50 am 14.03.2024", nil},
		{"a code the target adds is not reported", "Use the cable", "Verwenden Sie das USB3-Kabel", nil},
		{"percent in prose is no variable", "Get 20% discount", "Erhalten Sie 20 % Rabatt", nil},
		{"a code inside a target compound is present", "Plug in the MP3 player", "Schließen Sie den MP3-Player an", nil},
		{"a number glued to its unit is left to the number checks", "Get 5GB free", "5 GB gratis", nil},
		{"a variable right before a letter is present", "Wait %ds, size %dx%d", "Warte %ds, Größe %dx%d", nil},
		{"a changed code variant is missing", "Order XJ-500B and SKU_1234", "Bestellen XJ-500B-2 und SKU_1234_B", []string{"Placeable(s) missing from target: XJ-500B, SKU_1234"}},
		{"a code against Thai text is present", "Model XJ-500B ready", "รุ่นXJ-500Bพร้อม", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runPlaceables(c.source, c.target, "")
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestCheckPlaceables_SubChecks(t *testing.T) {
	source, target := "Mail test@test.com about XJ-500B, {name}", "Schreiben Sie"
	cases := []struct{ config, want string }{
		{`{"urls_emails":true,"codes":true,"variables":true}`, "Placeable(s) missing from target: test@test.com, XJ-500B, {name}"},
		{`{"urls_emails":false}`, "Placeable(s) missing from target: XJ-500B, {name}"},
		{`{"codes":false}`, "Placeable(s) missing from target: test@test.com, {name}"},
		{`{"variables":false}`, "Placeable(s) missing from target: test@test.com, XJ-500B"},
		{`{"urls_emails":false,"codes":false,"variables":false}`, ""},
		{`not json`, "Placeable(s) missing from target: test@test.com, XJ-500B, {name}"},
	}
	for _, c := range cases {
		if got := strings.Join(runPlaceables(source, target, c.config), "\n"); got != c.want {
			t.Errorf("config %s: got %q, want %q", c.config, got, c.want)
		}
	}
}

func TestCheckPlaceables_OffWhenRuleOff(t *testing.T) {
	issues := CheckAllWithParams(context.Background(), CheckAllParams{Source: "Hello {name}", Target: "Hallo", Rules: map[string]string{}})
	if len(issues) != 0 {
		t.Fatalf("got %+v", issues)
	}
}

func TestValidatePlaceablesConfig(t *testing.T) {
	for raw, want := range map[string]PlaceablesConfig{
		``:                    DefaultPlaceablesConfig(),
		`{}`:                  DefaultPlaceablesConfig(),
		`{"codes":false}`:     {URLsEmails: true, Codes: false, Variables: true},
		`{"variables":false}`: {URLsEmails: true, Codes: true, Variables: false},
	} {
		out, err := ValidatePlaceablesConfig([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		var got PlaceablesConfig
		if err := json.Unmarshal(out, &got); err != nil || got != want {
			t.Errorf("%s: saved %s, want %+v", raw, out, want)
		}
	}
	for _, raw := range []string{`{"patterns":[]}`, `{"codes":"yes"}`, `[`} {
		_, err := ValidatePlaceablesConfig([]byte(raw))
		var ce *httputil.ClientError
		if !errors.As(err, &ce) {
			t.Errorf("%s: got %v, want a client error", raw, err)
		}
	}
}
