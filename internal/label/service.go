package label

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// The label services, shared by the /api/labels handlers and the MCP label
// tools. Caller mistakes come back as httputil.ClientError, a refusal the
// current state causes (duplicate, in use, built in) as httputil.ConflictError,
// an unknown or foreign id as pgx.ErrNoRows.

// Value is one allowed value of a label. InUse says a project has chosen it,
// which is what keeps its label from being deleted; the value itself can
// always be deleted, and the projects that picked it lose the pick.
type Value struct {
	ID           int64  `json:"id"`
	Value        string `json:"value"`
	InUse        bool   `json:"in_use"`
	ProjectCount int64  `json:"project_count"`
}

// Label is a label definition with its values.
type Label struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Built in (customer, domain): its values are managed like any other
	// label's, but the label itself cannot be deleted.
	System bool `json:"system"`
	// SystemKey names a built-in label: "customer" or "domain". Empty otherwise.
	SystemKey string `json:"system_key,omitempty"`
	// True when any project carries one of its values.
	InUse  bool    `json:"in_use"`
	Values []Value `json:"values"`
}

// Pick is one label choice on a project or template, with the names so a
// client can render it without the label list.
type Pick struct {
	LabelDefinitionID int64  `json:"label_definition_id"`
	LabelValueID      int64  `json:"label_value_id"`
	LabelName         string `json:"label_name"`
	LabelValue        string `json:"label_value"`
}

// List returns every live label in the tenant with its values, labels by name
// and values within a label by value, both case-insensitive.
func List(ctx context.Context, q db.Querier, tenantID int64) ([]Label, error) {
	defs, err := q.ListLabelDefinitions(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	values, err := q.ListLabelValues(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list label values: %w", err)
	}
	usage, err := q.ListLabelValueUsage(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list label usage: %w", err)
	}
	projectCounts := make(map[int64]int64, len(usage))
	for _, u := range usage {
		projectCounts[u.LabelValueID] = u.ProjectCount
	}

	byDef := make(map[int64][]Value, len(defs))
	usedDefs := make(map[int64]bool, len(defs))
	for _, v := range values {
		n := projectCounts[v.ID]
		byDef[v.LabelDefinitionID] = append(byDef[v.LabelDefinitionID], Value{
			ID: v.ID, Value: v.Value, InUse: n > 0, ProjectCount: n,
		})
		if n > 0 {
			usedDefs[v.LabelDefinitionID] = true
		}
	}
	out := make([]Label, 0, len(defs))
	for _, d := range defs {
		vals := byDef[d.ID]
		if vals == nil {
			vals = []Value{}
		}
		l := Label{ID: d.ID, Name: d.Name, System: d.SystemKey != nil, InUse: usedDefs[d.ID], Values: vals}
		if d.SystemKey != nil {
			l.SystemKey = *d.SystemKey
		}
		out = append(out, l)
	}
	return out, nil
}

// Create inserts a label and its first values in one transaction — a label
// created with values is one user action. Blank values are dropped.
func Create(ctx context.Context, pool *pgxpool.Pool, tenantID int64, rawName string, rawValues []string) (Label, error) {
	name, err := cleanText(rawName, "name", maxNameLen)
	if err != nil {
		return Label{}, err
	}
	if len(rawValues) > maxValuesPerCreate {
		return Label{}, httputil.NewClientError("too many values (max 200)")
	}
	values, err := cleanValues(rawValues)
	if err != nil {
		return Label{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Label{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)

	def, err := txq.CreateLabelDefinition(ctx, db.CreateLabelDefinitionParams{TenantID: tenantID, Name: name})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return Label{}, httputil.NewConflictError("a label with this name already exists")
		}
		return Label{}, fmt.Errorf("create label: %w", err)
	}
	out := Label{ID: def.ID, Name: def.Name, Values: []Value{}}
	for _, v := range values {
		created, err := txq.CreateLabelValue(ctx, db.CreateLabelValueParams{LabelDefinitionID: def.ID, Value: v})
		if err != nil {
			if httputil.IsUniqueViolation(err) {
				return Label{}, httputil.NewConflictError("duplicate value: " + v)
			}
			return Label{}, fmt.Errorf("create label value: %w", err)
		}
		out.Values = append(out.Values, Value{ID: created.ID, Value: created.Value})
	}
	if err := tx.Commit(ctx); err != nil {
		return Label{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// AddValue adds one allowed value to a label of the tenant.
func AddValue(ctx context.Context, q db.Querier, tenantID, labelID int64, raw string) (Value, error) {
	value, err := cleanText(raw, "value", maxValueLen)
	if err != nil {
		return Value{}, err
	}
	// Tenant-scoped first, so a foreign label id is not-found rather than an
	// insert against someone else's data.
	if _, err := q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: labelID, TenantID: tenantID}); err != nil {
		return Value{}, err
	}
	created, err := q.CreateLabelValue(ctx, db.CreateLabelValueParams{LabelDefinitionID: labelID, Value: value})
	if err != nil {
		if httputil.IsUniqueViolation(err) {
			return Value{}, httputil.NewConflictError("this label already has that value")
		}
		return Value{}, fmt.Errorf("create label value: %w", err)
	}
	return Value{ID: created.ID, Value: created.Value}, nil
}

// Delete soft-deletes a label, but only while no project carries one of its
// values and it is not built in. A label that is already gone (or foreign) is a no-op, so the
// call is idempotent and cannot probe for existence.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	rows, err := q.SoftDeleteLabelDefinition(ctx, db.SoftDeleteLabelDefinitionParams{ID: id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete label: %w", err)
	}
	if rows > 0 {
		return nil
	}
	return deleteRefusal(ctx, q, tenantID, id)
}

// CheckDeletable returns the refusal Delete would give, without deleting.
func CheckDeletable(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	return deleteRefusal(ctx, q, tenantID, id)
}

// deleteRefusal explains why a label cannot go: built in, or one of its values
// in use. nil when neither holds.
func deleteRefusal(ctx context.Context, q db.Querier, tenantID, id int64) error {
	def, err := q.GetLabelDefinition(ctx, db.GetLabelDefinitionParams{ID: id, TenantID: tenantID})
	if err == nil && def.SystemKey != nil {
		return httputil.NewConflictError("Customer and Domain are built in and cannot be deleted")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get label: %w", err)
	}
	count, err := q.CountProjectsForLabel(ctx, db.CountProjectsForLabelParams{LabelDefinitionID: id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("count projects: %w", err)
	}
	if count > 0 {
		return httputil.NewConflictError(fmt.Sprintf(
			"%s still using values of this label — delete those values first, or change the projects", projectsPhrase(count)))
	}
	return nil
}

// DeleteValue soft-deletes one value and, in the same transaction, unsets it
// on every project and template that picked it. A value that is already gone
// (or foreign) is a no-op, so the call is idempotent and cannot probe for
// existence.
func DeleteValue(ctx context.Context, pool *pgxpool.Pool, tenantID, valueID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)

	rows, err := txq.SoftDeleteLabelValue(ctx, db.SoftDeleteLabelValueParams{ID: valueID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete label value: %w", err)
	}
	if rows == 0 {
		return nil
	}
	if err := txq.DeleteProjectLabelsForValue(ctx, db.DeleteProjectLabelsForValueParams{LabelValueID: valueID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("unset value on projects: %w", err)
	}
	if err := txq.DeleteTemplateLabelsForValue(ctx, db.DeleteTemplateLabelsForValueParams{LabelValueID: valueID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("unset value on templates: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ValueForDelete returns the value DeleteValue would delete and how many
// projects would lose it.
func ValueForDelete(ctx context.Context, q db.Querier, tenantID, valueID int64) (db.LabelValue, int64, error) {
	v, err := q.GetLabelValue(ctx, db.GetLabelValueParams{ID: valueID, TenantID: tenantID})
	if err != nil {
		return db.LabelValue{}, 0, err
	}
	count, err := q.CountProjectsForLabelValue(ctx, db.CountProjectsForLabelValueParams{LabelValueID: valueID, TenantID: tenantID})
	if err != nil {
		return db.LabelValue{}, 0, fmt.Errorf("count projects: %w", err)
	}
	return v, count, nil
}

// ProjectPicks returns a project's picks in label name order. Picks whose
// label or value was retired are left out (the query filters both).
func ProjectPicks(ctx context.Context, q db.Querier, tenantID, projectID int64) ([]Pick, error) {
	rows, err := q.ListProjectLabels(ctx, db.ListProjectLabelsParams{ProjectID: projectID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list project labels: %w", err)
	}
	out := make([]Pick, 0, len(rows))
	for _, row := range rows {
		out = append(out, Pick{
			LabelDefinitionID: row.LabelDefinitionID, LabelValueID: row.LabelValueID,
			LabelName: row.LabelName, LabelValue: row.LabelValue,
		})
	}
	return out, nil
}

// SetProjectPicks replaces a project's whole set of picks with the given
// label values, in one transaction. The value decides the label.
func SetProjectPicks(ctx context.Context, pool *pgxpool.Pool, q db.Querier, tenantID, projectID int64, valueIDs []int64) ([]Pick, error) {
	if err := database.InTx(ctx, pool, func(_ pgx.Tx, txq db.Querier) error {
		return ReplaceProjectPicks(ctx, txq, tenantID, projectID, valueIDs)
	}); err != nil {
		return nil, err
	}
	return ProjectPicks(ctx, q, tenantID, projectID)
}

// ReplaceProjectPicks is SetProjectPicks on the caller's transaction: the
// project and every value must belong to the tenant, both checked before the
// first write; then the project's picks become exactly these.
func ReplaceProjectPicks(ctx context.Context, q db.Querier, tenantID, projectID int64, valueIDs []int64) error {
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		return err
	}
	picks, err := resolvePicks(ctx, q, tenantID, valueIDs)
	if err != nil {
		return err
	}
	if err := q.DeleteProjectLabels(ctx, projectID); err != nil {
		return fmt.Errorf("clear project labels: %w", err)
	}
	for _, p := range picks {
		if err := q.SetProjectLabel(ctx, db.SetProjectLabelParams{
			ProjectID: projectID, LabelDefinitionID: p.definitionID, LabelValueID: p.valueID,
		}); err != nil {
			return fmt.Errorf("set project label: %w", err)
		}
	}
	return nil
}

// pick is a resolved label choice: the value, and the label it belongs to.
type pick struct{ definitionID, valueID int64 }

// resolvePicks verifies every value in the tenant before anything is written,
// so a bad id in the middle cannot leave a half-applied set. The value
// decides the label: trusting a client's label id would let a pick claim one
// label while pointing at another's value.
func resolvePicks(ctx context.Context, q db.Querier, tenantID int64, valueIDs []int64) ([]pick, error) {
	picks := make([]pick, 0, len(valueIDs))
	seen := make(map[int64]bool, len(valueIDs))
	for _, id := range valueIDs {
		v, err := q.GetLabelValue(ctx, db.GetLabelValueParams{ID: id, TenantID: tenantID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, httputil.NewClientError("label value not found in this tenant")
			}
			return nil, fmt.Errorf("get label value: %w", err)
		}
		if seen[v.LabelDefinitionID] {
			return nil, httputil.NewClientError("a label can only be set once")
		}
		seen[v.LabelDefinitionID] = true
		picks = append(picks, pick{definitionID: v.LabelDefinitionID, valueID: v.ID})
	}
	return picks, nil
}

// projectsPhrase renders the count as the subject of a refusal message.
func projectsPhrase(n int64) string {
	if n == 1 {
		return "1 project is"
	}
	return fmt.Sprintf("%d projects are", n)
}

// cleanText trims and length-checks one free-text field.
func cleanText(raw, field string, max int) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", httputil.NewClientError(field + " is required")
	}
	if len(s) > max {
		return "", httputil.NewClientError(field + " too long (max 200 characters)")
	}
	return s, nil
}

// cleanValues trims a value list and drops blanks — a picker that lets the
// user type sends the odd empty string. Duplicates are left to the unique
// index.
func cleanValues(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s := strings.TrimSpace(v)
		if s == "" {
			continue
		}
		if len(s) > maxValueLen {
			return nil, httputil.NewClientError("value too long (max 200 characters)")
		}
		out = append(out, s)
	}
	return out, nil
}
