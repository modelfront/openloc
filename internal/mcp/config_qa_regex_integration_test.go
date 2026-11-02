package mcp_test

import (
	"context"
	"strings"
	"testing"
)

type cfgQARegexOut struct {
	QAProfileID int64 `json:"qa_profile_id"`
	Rules       []struct {
		RuleID    int64          `json:"rule_id"`
		CheckType string         `json:"check_type"`
		Config    map[string]any `json:"config"`
	} `json:"rules"`
}

// TestIntegrationMCPConfigQARegexRules pins regex rules through the MCP
// tools: created with the profile, added and replaced by rule_id, validated
// like the app, deleted by id, and out of reach for another tenant.
func TestIntegrationMCPConfigQARegexRules(t *testing.T) {
	c := newConfigFixture(t)
	ctx := context.Background()
	regex := func(message, pattern string) map[string]any {
		return map[string]any{"check_type": "regex", "config": map[string]any{
			"name": "Unit space", "mode": "target_must_not_match", "pattern": pattern,
			"message": message, "target_languages": []string{"fr"},
		}}
	}

	var p cfgQARegexOut
	if isErr, text := callTool(t, c.asManager, "openloc_create_qa_profile", map[string]any{
		"name": "Cfg regex", "rules": []map[string]any{regex("first", `\d (kg|cm)`), regex("second", `\d %`)},
	}, &p); isErr {
		t.Fatalf("create_qa_profile: %s", text)
	}
	if len(p.Rules) != 2 || p.Rules[0].RuleID == 0 || p.Rules[0].CheckType != "regex" {
		t.Fatalf("two regex rules with ids expected: %+v", p.Rules)
	}
	cfgMustRefuse(t, c.asManager, "openloc_create_qa_profile", map[string]any{
		"name": "Cfg bad regex", "rules": []map[string]any{regex("x", `\d (kg`)},
	}, "rule 1: pattern: missing closing )")

	edit := regex("edited message", `\d (kg|cm|mm)`)
	edit["rule_id"] = p.Rules[0].RuleID
	upsert := map[string]any{"qa_profile_id": p.QAProfileID, "rules": []map[string]any{edit, regex("third", `\d h`)}}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_upsert_qa_rules", upsert, &p); isErr {
		t.Fatalf("upsert_qa_rules: %s", text)
	}
	rules, err := c.q.ListQARules(ctx, p.QAProfileID)
	if err != nil || len(rules) != 3 {
		t.Fatalf("want 3 regex rules (one replaced, one added), got %d (%v)", len(rules), err)
	}
	if !strings.Contains(string(rules[0].Config), "edited message") {
		t.Errorf("rule %d not replaced in place: %s", rules[0].ID, rules[0].Config)
	}

	c.refresh(t)
	cfgMustNotFind(t, c.asOwnerB, "openloc_delete_qa_rule", map[string]any{"qa_profile_id": p.QAProfileID, "rule_id": rules[1].ID})
	cfgPreviewOnly(t, c.asManager, "openloc_delete_qa_rule", map[string]any{"qa_profile_id": p.QAProfileID, "rule_id": rules[1].ID})
	if after, _ := c.q.ListQARules(ctx, p.QAProfileID); len(after) != 3 {
		t.Fatalf("the delete preview deleted a rule")
	}
	if isErr, text := confirmedCall(t, c.asManager, "openloc_delete_qa_rule", map[string]any{"qa_profile_id": p.QAProfileID, "rule_id": rules[1].ID}, &p); isErr {
		t.Fatalf("delete_qa_rule: %s", text)
	}
	if len(p.Rules) != 2 {
		t.Errorf("want 2 rules after the delete, got %+v", p.Rules)
	}
}
