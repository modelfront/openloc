package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// ProfileFile is the JSON file a QA profile is exported to and imported from.
// Text is kept exactly as stored, special spaces such as U+00A0 and U+202F
// included.
type ProfileFile struct {
	Format        string     `json:"format"`
	FormatVersion int        `json:"format_version"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Rules         []FileRule `json:"rules"`
}

// FileRule is one rule of a ProfileFile.
type FileRule struct {
	CheckType string          `json:"check_type"`
	Enabled   bool            `json:"enabled"`
	Severity  string          `json:"severity"`
	Config    json.RawMessage `json:"config"`
}

// Identity of the profile file format.
const (
	ProfileFileFormat  = "openloc-qa-profile"
	ProfileFileVersion = 1
)

// ExportProfile returns a profile and its rules as a ProfileFile.
func ExportProfile(ctx context.Context, q db.Querier, tenantID, id int64) (ProfileFile, error) {
	profile, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: id, TenantID: tenantID})
	if err != nil {
		return ProfileFile{}, err
	}
	rules, err := q.ListQARules(ctx, id)
	if err != nil {
		return ProfileFile{}, fmt.Errorf("list QA rules: %w", err)
	}
	out := ProfileFile{
		Format: ProfileFileFormat, FormatVersion: ProfileFileVersion,
		Name: profile.Name, Description: profile.Description,
		Rules: make([]FileRule, 0, len(rules)),
	}
	for _, r := range rules {
		cfg := json.RawMessage(r.Config)
		if len(cfg) == 0 {
			cfg = json.RawMessage(`{}`)
		}
		out.Rules = append(out.Rules, FileRule{CheckType: r.CheckType, Enabled: r.Enabled, Severity: r.Severity, Config: cfg})
	}
	return out, nil
}

// EncodeProfileFile writes f as indented JSON without HTML escaping, so the
// file reads as the user typed it.
func EncodeProfileFile(f ProfileFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fileRuleInputs checks a file's header and turns its rules into RuleInputs.
func fileRuleInputs(f ProfileFile) ([]RuleInput, error) {
	if f.Format != ProfileFileFormat {
		return nil, httputil.NewClientError(fmt.Sprintf("not a QA profile file (format must be %q)", ProfileFileFormat))
	}
	if f.FormatVersion != ProfileFileVersion {
		return nil, httputil.NewClientError(fmt.Sprintf("unsupported format_version %d (this server reads %d)", f.FormatVersion, ProfileFileVersion))
	}
	out := make([]RuleInput, 0, len(f.Rules))
	for _, r := range f.Rules {
		enabled := r.Enabled
		out = append(out, RuleInput{CheckType: r.CheckType, Enabled: &enabled, Severity: r.Severity, Config: r.Config})
	}
	return out, nil
}

// ImportProfile creates a new profile from a file. A name already in use gets
// " (2)", " (3)" … appended.
func ImportProfile(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID int64, f ProfileFile) (db.QaProfile, error) {
	rules, err := fileRuleInputs(f)
	if err != nil {
		return db.QaProfile{}, err
	}
	existing, err := q.ListQAProfiles(ctx, tenantID)
	if err != nil {
		return db.QaProfile{}, fmt.Errorf("list QA profiles: %w", err)
	}
	taken := make(map[string]bool, len(existing))
	for _, p := range existing {
		taken[p.Name] = true
	}
	name := f.Name
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s (%d)", f.Name, n)
	}
	profile, _, err := CreateProfileWithRules(ctx, pool, tenantID, ProfileInput{Name: name, Description: f.Description}, rules)
	return profile, err
}

// ReplaceProfileRules replaces every rule of a profile with the rules of a
// file, in one transaction; the profile's name and description stay. Every
// rule is validated before anything is written.
func ReplaceProfileRules(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, profileID int64, f ProfileFile) ([]db.QaRule, error) {
	rules, err := fileRuleInputs(f)
	if err != nil {
		return nil, err
	}
	if _, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profileID, TenantID: tenantID}); err != nil {
		return nil, err
	}
	var writes []ruleWrite
	if len(rules) > 0 {
		if writes, err = rulesParams(ctx, profileID, rules); err != nil {
			return nil, err
		}
	}
	var out []db.QaRule
	err = inPoolTx(ctx, pool, func(txq db.Querier) error {
		if err := txq.DeleteQARulesForProfile(ctx, profileID); err != nil {
			return fmt.Errorf("delete QA rules: %w", err)
		}
		var err error
		out, err = writeRules(ctx, txq, tenantID, profileID, writes)
		return err
	})
	return out, err
}
