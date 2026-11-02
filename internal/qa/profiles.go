package qa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Transport-free QA profile and rule writes, shared by the REST handler and the
// MCP tools. Caller mistakes come back as httputil.ClientError, an in-use
// refusal as httputil.ConflictError, and an id outside the tenant as
// pgx.ErrNoRows.

// ProfileInput is a QA profile's writable shape (create and update).
type ProfileInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CheckTypes are the rules a profile can switch on.
var CheckTypes = map[string]bool{
	"tag_checks":                 true,
	"number_consistency":         true,
	"number_format":              true,
	"date_consistency":           true,
	"time_consistency":           true,
	"unit_conversion":            true,
	"empty_target":               true,
	"terminology":                true,
	"consistency":                true,
	"punctuation":                true,
	"target_identical_to_source": true,
	"forbidden_terms":            true,
	PlaceablesCheckType:          true,
}

// Severities are the levels a rule can report at.
var Severities = map[string]bool{"error": true, "warning": true, "info": true}

// CheckTypeList returns CheckTypes sorted, for messages and tool schemas.
func CheckTypeList() []string {
	out := make([]string, 0, len(CheckTypes))
	for k := range CheckTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CreateProfile inserts a profile with no rules.
func CreateProfile(ctx context.Context, q db.Querier, tenantID int64, in ProfileInput) (db.QaProfile, error) {
	if in.Name == "" {
		return db.QaProfile{}, httputil.NewClientError("name is required")
	}
	return q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID: tenantID, Name: in.Name, Description: in.Description,
	})
}

// CreateProfileWithRules inserts a profile and its first rules in one
// transaction. Every rule is validated before anything is written.
func CreateProfileWithRules(ctx context.Context, pool *pgxpool.Pool, tenantID int64, in ProfileInput, rules []RuleInput) (db.QaProfile, []db.QaRule, error) {
	if in.Name == "" {
		return db.QaProfile{}, nil, httputil.NewClientError("name is required")
	}
	var writes []ruleWrite
	if len(rules) > 0 {
		var err error
		if writes, err = rulesParams(ctx, 0, rules); err != nil {
			return db.QaProfile{}, nil, err
		}
	}
	var profile db.QaProfile
	var out []db.QaRule
	err := inPoolTx(ctx, pool, func(txq db.Querier) error {
		var err error
		if profile, err = CreateProfile(ctx, txq, tenantID, in); err != nil {
			return err
		}
		out, err = writeRules(ctx, txq, tenantID, profile.ID, writes)
		return err
	})
	if err != nil {
		return db.QaProfile{}, nil, err
	}
	return profile, out, nil
}

// UpdateProfile replaces a profile's name and description.
func UpdateProfile(ctx context.Context, q db.Querier, tenantID, id int64, in ProfileInput) (db.QaProfile, error) {
	if _, err := ValidateProfileUpdate(ctx, q, tenantID, id, in); err != nil {
		return db.QaProfile{}, err
	}
	return q.UpdateQAProfile(ctx, db.UpdateQAProfileParams{ID: id, TenantID: tenantID, Name: in.Name, Description: in.Description})
}

// ValidateProfileUpdate checks an edit exactly as UpdateProfile does, without
// writing it, and returns the profile as it is now.
func ValidateProfileUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in ProfileInput) (db.QaProfile, error) {
	profile, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.QaProfile{}, err
	}
	if in.Name == "" {
		return db.QaProfile{}, httputil.NewClientError("name is required")
	}
	return profile, nil
}

// ProfileUsage counts the settings profiles — projects and templates — that
// use a QA profile. Callers verify the profile's tenant first.
func ProfileUsage(ctx context.Context, q db.Querier, id int64) (int64, error) {
	refs, err := q.CountQAProfileReferences(ctx, &id)
	if err != nil {
		return 0, fmt.Errorf("count QA profile references: %w", err)
	}
	return int64(refs), nil
}

// CheckDeleteProfile returns the profile when it may be deleted: refused with
// httputil.ConflictError while any project or template uses it.
func CheckDeleteProfile(ctx context.Context, q db.Querier, tenantID, id int64) (db.QaProfile, error) {
	profile, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.QaProfile{}, err
	}
	refs, err := ProfileUsage(ctx, q, id)
	if err != nil {
		return db.QaProfile{}, err
	}
	if refs > 0 {
		return db.QaProfile{}, httputil.NewConflictError(fmt.Sprintf("cannot delete: %d project(s) use this QA profile", refs))
	}
	return profile, nil
}

// DeleteProfile soft-deletes a profile after CheckDeleteProfile. Its rules
// stay with the row.
func DeleteProfile(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := CheckDeleteProfile(ctx, q, tenantID, id); err != nil {
		return err
	}
	return q.SoftDeleteQAProfile(ctx, db.SoftDeleteQAProfileParams{ID: id, TenantID: tenantID})
}

// RuleInput is one rule of a profile. Enabled nil means true; an empty
// severity means error; a nil config means {}. RuleID names the regex rule to
// replace; a regex rule without one is created.
type RuleInput struct {
	RuleID    int64           `json:"rule_id,omitempty"`
	CheckType string          `json:"check_type"`
	Enabled   *bool           `json:"enabled"`
	Severity  string          `json:"severity"`
	Config    json.RawMessage `json:"config"`
}

// ruleWrite is a validated rule: the row to write and, for a regex rule being
// replaced, its id.
type ruleWrite struct {
	ruleID int64
	params db.UpsertQARuleParams
}

var errTooManyRegexRules = httputil.NewClientError(fmt.Sprintf("a profile holds at most %d regex rules", MaxRegexRulesPerProfile))

// UpsertRule creates or replaces one rule of a profile: the rule of a
// built-in check type, or a regex rule.
func UpsertRule(ctx context.Context, q db.Querier, tenantID, profileID int64, in RuleInput) (db.QaRule, error) {
	if _, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID}); err != nil {
		return db.QaRule{}, err
	}
	w, err := ruleParams(ctx, profileID, in)
	if err != nil {
		return db.QaRule{}, err
	}
	rules, err := writeRules(ctx, q, tenantID, profileID, []ruleWrite{w})
	if err != nil {
		return db.QaRule{}, err
	}
	return rules[0], nil
}

// DeleteRule deletes a regex rule of a profile. A built-in rule is switched
// off instead (enabled false), so this answers pgx.ErrNoRows for one.
func DeleteRule(ctx context.Context, q db.Querier, tenantID, profileID, ruleID int64) error {
	n, err := q.DeleteQARegexRule(ctx, db.DeleteQARegexRuleParams{ID: ruleID, ProfileID: profileID, TenantID: tenantID})
	if err != nil {
		return err
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ruleParams validates a rule and applies its defaults.
func ruleParams(ctx context.Context, profileID int64, in RuleInput) (ruleWrite, error) {
	if in.CheckType == "" {
		return ruleWrite{}, httputil.NewClientError("check_type is required")
	}
	config := []byte(in.Config)
	if in.CheckType == RegexCheckType {
		var err error
		if config, err = ValidateRegexRuleConfig(ctx, config); err != nil {
			return ruleWrite{}, err
		}
	} else {
		if !CheckTypes[in.CheckType] {
			return ruleWrite{}, httputil.NewClientError("invalid check_type")
		}
		if in.RuleID != 0 {
			return ruleWrite{}, httputil.NewClientError("rule_id is only for regex rules")
		}
		if len(config) == 0 {
			config = []byte("{}")
		}
		if in.CheckType == "number_format" {
			var err error
			if config, err = ValidateNumberFormatConfig(config); err != nil {
				return ruleWrite{}, err
			}
		}
		if in.CheckType == PlaceablesCheckType {
			var err error
			if config, err = ValidatePlaceablesConfig(config); err != nil {
				return ruleWrite{}, err
			}
		}
	}
	severity := strings.TrimSpace(in.Severity)
	if severity == "" {
		severity = "error"
	}
	if !Severities[severity] {
		return ruleWrite{}, httputil.NewClientError("invalid severity (must be error, warning, or info)")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return ruleWrite{ruleID: in.RuleID, params: db.UpsertQARuleParams{
		ProfileID: profileID, CheckType: in.CheckType, Enabled: enabled,
		Severity: severity, Config: config,
	}}, nil
}

// writeRules writes validated rules of a tenant-verified profile: a built-in
// rule is upserted on its check type, a regex rule is created, or replaced by
// id. New regex rules may not take the profile past MaxRegexRulesPerProfile.
func writeRules(ctx context.Context, q db.Querier, tenantID, profileID int64, writes []ruleWrite) ([]db.QaRule, error) {
	creates := 0
	for _, w := range writes {
		if w.params.CheckType == RegexCheckType && w.ruleID == 0 {
			creates++
		}
	}
	if creates > 0 {
		have, err := q.CountQARegexRules(ctx, profileID)
		if err != nil {
			return nil, fmt.Errorf("count regex rules: %w", err)
		}
		if have+int64(creates) > MaxRegexRulesPerProfile {
			return nil, errTooManyRegexRules
		}
	}
	out := make([]db.QaRule, 0, len(writes))
	for _, w := range writes {
		p := w.params
		p.ProfileID = profileID
		var rule db.QaRule
		var err error
		switch {
		case p.CheckType != RegexCheckType:
			rule, err = q.UpsertQARule(ctx, p)
		case w.ruleID == 0:
			rule, err = q.CreateQARegexRule(ctx, db.CreateQARegexRuleParams{
				ProfileID: profileID, Enabled: p.Enabled, Severity: p.Severity, Config: p.Config,
			})
		default:
			rule, err = q.UpdateQARegexRule(ctx, db.UpdateQARegexRuleParams{
				ID: w.ruleID, ProfileID: profileID, TenantID: tenantID,
				Enabled: p.Enabled, Severity: p.Severity, Config: p.Config,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, httputil.NewClientError(fmt.Sprintf("rule_id %d is not a regex rule of this profile", w.ruleID))
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// UpsertRules creates or replaces several rules of one profile at once. Every
// rule is validated before any is written, and the writes share a
// transaction, so a bad rule leaves the profile as it was.
func UpsertRules(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, profileID int64, in []RuleInput) ([]db.QaRule, error) {
	if _, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	writes, err := rulesParams(ctx, profileID, in)
	if err != nil {
		return nil, err
	}
	var out []db.QaRule
	err = inPoolTx(ctx, pool, func(txq db.Querier) error {
		out, err = writeRules(ctx, txq, tenantID, profileID, writes)
		return err
	})
	return out, err
}

// ValidateRules checks a batch exactly as UpsertRules does, without writing
// it: the profile is this tenant's, there is at least one rule, each is valid,
// and no check type or regex rule appears twice. It returns the rule count.
func ValidateRules(ctx context.Context, q db.Querier, tenantID, profileID int64, in []RuleInput) (db.QaProfile, int, error) {
	profile, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID})
	if err != nil {
		return db.QaProfile{}, 0, err
	}
	writes, err := rulesParams(ctx, profileID, in)
	if err != nil {
		return db.QaProfile{}, 0, err
	}
	return profile, len(writes), nil
}

func rulesParams(ctx context.Context, profileID int64, in []RuleInput) ([]ruleWrite, error) {
	if len(in) == 0 {
		return nil, httputil.NewClientError("at least one rule is required")
	}
	// Checked before any pattern is test-run, so the batch size bounds the work.
	regexRules := 0
	for _, r := range in {
		if r.CheckType == RegexCheckType {
			regexRules++
		}
	}
	if regexRules > MaxRegexRulesPerProfile {
		return nil, errTooManyRegexRules
	}
	writes := make([]ruleWrite, 0, len(in))
	seen := make(map[string]bool, len(in))
	seenID := make(map[int64]bool)
	for i, r := range in {
		w, err := ruleParams(ctx, profileID, r)
		if err != nil {
			msg, _ := httputil.AsClientError(err)
			return nil, httputil.NewClientError(fmt.Sprintf("rule %d: %s", i+1, msg))
		}
		if w.params.CheckType == RegexCheckType {
			if w.ruleID != 0 && seenID[w.ruleID] {
				return nil, httputil.NewClientError(fmt.Sprintf("rule %d: rule_id %d is listed twice", i+1, w.ruleID))
			}
			seenID[w.ruleID] = true
		} else {
			if seen[w.params.CheckType] {
				return nil, httputil.NewClientError(fmt.Sprintf("rule %d: %s is listed twice", i+1, w.params.CheckType))
			}
			seen[w.params.CheckType] = true
		}
		writes = append(writes, w)
	}
	return writes, nil
}

// inPoolTx runs fn in a transaction on pool.
func inPoolTx(ctx context.Context, pool *pgxpool.Pool, fn func(q db.Querier) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
