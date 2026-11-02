package testutil

import (
	"strings"

	"openloc/internal/segmentation"
)

// CostlySegmentationText is ordinary prose on which CostlySegmentationRules
// run over the work budget.
var CostlySegmentationText = strings.Repeat("A long sentence without the letter. ", 30)

// CostlySegmentationRules is a valid rule set that is too costly to run: its
// MaxRules rules are tried at every position and read their whole window.
func CostlySegmentationRules() segmentation.RuleSet {
	rs := segmentation.RuleSet{Rules: make([]segmentation.Rule, segmentation.MaxRules)}
	for i := range rs.Rules {
		rs.Rules[i] = segmentation.Rule{Before: `z(?s).*`, After: ``, Break: true}
	}
	return rs
}
