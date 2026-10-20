package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	"empty_target":               true,
	"terminology":                true,
	"consistency":                true,
	"punctuation":                true,
	"target_identical_to_source": true,
	"forbidden_terms":            true,
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
	var params []db.UpsertQARuleParams
	if len(rules) > 0 {
		var err error
		if params, err = rulesParams(0, rules); err != nil {
			return db.QaProfile{}, nil, err
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.QaProfile{}, nil, err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)
	profile, err := CreateProfile(ctx, txq, tenantID, in)
	if err != nil {
		return db.QaProfile{}, nil, err
	}
	out := make([]db.QaRule, 0, len(params))
	for _, p := range params {
		p.ProfileID = profile.ID
		rule, err := txq.UpsertQARule(ctx, p)
		if err != nil {
			return db.QaProfile{}, nil, err
		}
		out = append(out, rule)
	}
	if err := tx.Commit(ctx); err != nil {
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
// severity means error; a nil config means {}.
type RuleInput struct {
	CheckType string          `json:"check_type"`
	Enabled   *bool           `json:"enabled"`
	Severity  string          `json:"severity"`
	Config    json.RawMessage `json:"config"`
}

// UpsertRule creates or replaces the rule for one check type on a profile.
func UpsertRule(ctx context.Context, q db.Querier, tenantID, profileID int64, in RuleInput) (db.QaRule, error) {
	if _, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID}); err != nil {
		return db.QaRule{}, err
	}
	params, err := ruleParams(profileID, in)
	if err != nil {
		return db.QaRule{}, err
	}
	return q.UpsertQARule(ctx, params)
}

// ruleParams validates a rule and applies its defaults.
func ruleParams(profileID int64, in RuleInput) (db.UpsertQARuleParams, error) {
	if in.CheckType == "" {
		return db.UpsertQARuleParams{}, httputil.NewClientError("check_type is required")
	}
	if !CheckTypes[in.CheckType] {
		return db.UpsertQARuleParams{}, httputil.NewClientError("invalid check_type")
	}
	severity := strings.TrimSpace(in.Severity)
	if severity == "" {
		severity = "error"
	}
	if !Severities[severity] {
		return db.UpsertQARuleParams{}, httputil.NewClientError("invalid severity (must be error, warning, or info)")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	config := []byte(in.Config)
	if len(config) == 0 {
		config = []byte("{}")
	}
	return db.UpsertQARuleParams{
		ProfileID: profileID, CheckType: in.CheckType, Enabled: enabled,
		Severity: severity, Config: config,
	}, nil
}

// UpsertRules creates or replaces several rules of one profile at once. Every
// rule is validated before any is written, and the writes share a
// transaction, so a bad rule leaves the profile as it was.
func UpsertRules(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, profileID int64, in []RuleInput) ([]db.QaRule, error) {
	_, params, err := ValidateRules(ctx, q, tenantID, profileID, in)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)
	out := make([]db.QaRule, 0, len(params))
	for _, p := range params {
		rule, err := txq.UpsertQARule(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateRules checks a batch exactly as UpsertRules does, without writing
// it: the profile is this tenant's, there is at least one rule, each is valid,
// and no check type appears twice.
func ValidateRules(ctx context.Context, q db.Querier, tenantID, profileID int64, in []RuleInput) (db.QaProfile, []db.UpsertQARuleParams, error) {
	profile, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID})
	if err != nil {
		return db.QaProfile{}, nil, err
	}
	params, err := rulesParams(profileID, in)
	if err != nil {
		return db.QaProfile{}, nil, err
	}
	return profile, params, nil
}

func rulesParams(profileID int64, in []RuleInput) ([]db.UpsertQARuleParams, error) {
	if len(in) == 0 {
		return nil, httputil.NewClientError("at least one rule is required")
	}
	params := make([]db.UpsertQARuleParams, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, r := range in {
		p, err := ruleParams(profileID, r)
		if err != nil {
			msg, _ := httputil.AsClientError(err)
			return nil, httputil.NewClientError(fmt.Sprintf("rule %d: %s", i+1, msg))
		}
		if seen[p.CheckType] {
			return nil, httputil.NewClientError(fmt.Sprintf("rule %d: %s is listed twice", i+1, p.CheckType))
		}
		seen[p.CheckType] = true
		params = append(params, p)
	}
	return params, nil
}
