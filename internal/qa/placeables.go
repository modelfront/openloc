package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/placeable"
)

// PlaceablesCheckType is the rule that compares a segment's placeables.
// Numbers, dates, times, measurements and amounts are the Numbers group's:
// they may be written differently in the target and still be right.
const PlaceablesCheckType = "placeables"

// DefaultPlaceablesConfig has every sub-check on.
func DefaultPlaceablesConfig() PlaceablesConfig {
	return PlaceablesConfig{URLsEmails: true, Codes: true, Variables: true}
}

// compared reports whether the config compares placeables of kind k.
func (c PlaceablesConfig) compared(k placeable.Kind) bool {
	switch k {
	case placeable.KindURL, placeable.KindEmail:
		return c.URLsEmails
	case placeable.KindCode:
		return c.Codes
	case placeable.KindVariable:
		return c.Variables
	}
	return false
}

// ValidatePlaceablesConfig checks a placeables config for saving and returns
// it with every sub-check spelled out. Unknown fields are a client error.
func ValidatePlaceablesConfig(raw []byte) ([]byte, error) {
	cfg := DefaultPlaceablesConfig()
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, httputil.NewClientError("placeables config: " + err.Error())
		}
	}
	return json.Marshal(cfg)
}

// placeablesConfig reads the rule's stored config. A config that does not
// parse means every sub-check, logged.
func placeablesConfig(ctx context.Context, raw []byte) PlaceablesConfig {
	cfg := DefaultPlaceablesConfig()
	if len(bytes.TrimSpace(raw)) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		logging.Warn(ctx, err, "placeables config malformed; using defaults", "qa.placeables_config_malformed")
		return DefaultPlaceablesConfig()
	}
	return cfg
}

// checkPlaceables reports, in one finding, the compared source placeables the
// target lacks and the addresses and variables the target has that the source
// lacks. A source placeable is in the target when placeable.Count finds its
// text there, counted: a variable written twice in the source must be there
// twice. A code the target adds may be the target
// language's own and is not reported.
func checkPlaceables(ctx context.Context, p CheckAllParams, severity string) []Issue {
	cfg := placeablesConfig(ctx, p.RuleConfigs[PlaceablesCheckType])
	var src, tgt []placeable.Placeable
	for _, s := range placeable.Find(p.Source, p.SourceLanguage) {
		if cfg.compared(s.Kind) {
			src = append(src, s)
		}
	}
	for _, t := range placeable.Find(p.Target, p.TargetLanguage) {
		if cfg.compared(t.Kind) {
			tgt = append(tgt, t)
		}
	}
	missing := notIn(src, p.Target)
	extra := unmatched(tgt, src, func(k placeable.Kind) bool { return k != placeable.KindCode })
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "Placeable(s) missing from target: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		label := "Target has placeable(s) not in source: "
		if len(missing) > 0 {
			label = "not in source: "
		}
		parts = append(parts, label+strings.Join(extra, ", "))
	}
	if len(parts) == 0 {
		return nil
	}
	// One finding: findings are stored one per check type.
	return []Issue{{CheckType: PlaceablesCheckType, Severity: severity, Message: strings.Join(parts, "; ")}}
}

// notIn returns the texts of the placeables that `text` does not have, each
// occurrence counted.
func notIn(placeables []placeable.Placeable, text string) []string {
	left := map[string]int{}
	var out []string
	for _, p := range placeables {
		n, seen := left[p.Text]
		if !seen {
			n = placeable.Count(text, p)
		}
		if n > 0 {
			left[p.Text] = n - 1
			continue
		}
		left[p.Text] = 0
		out = append(out, p.Text)
	}
	return out
}

// unmatched returns the texts of the placeables of `of` that `in` does not
// have, each occurrence counted, keeping only the kinds `report` accepts.
func unmatched(of, in []placeable.Placeable, report func(placeable.Kind) bool) []string {
	have := map[string]int{}
	for _, p := range in {
		have[p.Text]++
	}
	var out []string
	for _, p := range of {
		if have[p.Text] > 0 {
			have[p.Text]--
			continue
		}
		if report(p.Kind) {
			out = append(out, p.Text)
		}
	}
	return out
}
