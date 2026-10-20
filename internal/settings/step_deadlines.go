package settings

// The deadline half of workflow_step_assignments — a profile's workflow steps
// as they differ per target language. A project's jobs are file × target
// language, so one step can be due on a different date per language. The step
// row's own deadline is the value for every language without one:
//
//	deadline(step, language) = the row's deadline ?? step.Deadline ?? project.Deadline ?? none
//
// Resolution happens where a job_step_assignment is written (upload, appended
// steps, a blueprint edit), so every reader of jsa.deadline is untouched.
//
// Every column on the table is nullable, so a row set for one reason must
// never shadow the step's value for another — the queries filter
// `deadline IS NOT NULL` for exactly that reason. The migration explains the
// table's name.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/stepdef"
)

// LanguageDeadlineInput is one language's override as a client submits it.
// Deadline nil or "" removes the override, leaving the step's own value.
type LanguageDeadlineInput struct {
	TargetLanguage string  `json:"target_language"`
	Deadline       *string `json:"deadline"`
}

// LanguageDeadline is one stored override, as the API returns it.
type LanguageDeadline struct {
	TargetLanguage string `json:"target_language"`
	Deadline       string `json:"deadline"` // RFC 3339
}

// StepDeadlines resolves a step+language pair to the deadline an assignment
// created from it should carry. Build one per profile with LoadStepDeadlines
// and reuse it across every job of an upload.
type StepDeadlines struct {
	byStep map[int64]map[string]pgtype.Timestamptz
	// The project's own deadline, the last resort when nobody scheduled the
	// step. Zero for a template profile, which belongs to no project.
	project pgtype.Timestamptz
}

// Languages returns one step's per-language overrides, keyed by normalized
// language code. Nil when the step has none.
func (d StepDeadlines) Languages(stepID int64) map[string]pgtype.Timestamptz {
	return d.byStep[stepID]
}

// LoadStepDeadlines reads a profile's per-language deadlines in one round trip,
// plus the project deadline they fall back to.
func LoadStepDeadlines(ctx context.Context, q db.Querier, profileID int64) (StepDeadlines, error) {
	rows, err := q.ListWorkflowStepAssignmentsForProfile(ctx, profileID)
	if err != nil {
		return StepDeadlines{}, fmt.Errorf("list step language deadlines: %w", err)
	}
	out := StepDeadlines{byStep: map[int64]map[string]pgtype.Timestamptz{}}
	for _, r := range rows {
		if out.byStep[r.WorkflowStepID] == nil {
			out.byStep[r.WorkflowStepID] = map[string]pgtype.Timestamptz{}
		}
		out.byStep[r.WorkflowStepID][r.TargetLanguage] = r.Deadline
	}
	proj, err := q.GetProjectForProfile(ctx, profileID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StepDeadlines{}, fmt.Errorf("get project for profile: %w", err)
	}
	if err == nil {
		out.project = proj.Deadline
	}
	return out, nil
}

// For returns the deadline an assignment on this step, for this job's target
// language, should carry. An automated step completes itself, so it never carries one.
func (d StepDeadlines) For(step db.WorkflowStep, targetLanguage string) pgtype.Timestamptz {
	if stepdef.IsAutomated(step.StepType) {
		return pgtype.Timestamptz{}
	}
	if byLang, ok := d.byStep[step.ID]; ok {
		if ts, ok := byLang[langcode.Normalize(targetLanguage)]; ok {
			return ts
		}
	}
	if step.Deadline.Valid {
		return step.Deadline
	}
	return d.project
}

// StepWithDeadlines is a profile's step row with its language overrides
// attached — the shape every reader of a profile's workflow steps gets, so the
// project settings and template detail responses cannot drift apart.
type StepWithDeadlines struct {
	db.WorkflowStep
	LanguageDeadlines []LanguageDeadline `json:"language_deadlines"`
}

// ListStepsWithDeadlines reads a profile's steps and their language overrides.
func ListStepsWithDeadlines(ctx context.Context, q db.Querier, profileID int64) ([]StepWithDeadlines, error) {
	steps, err := q.ListSettingsWorkflowSteps(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("list workflow steps: %w", err)
	}
	byStep, err := LoadStepDeadlines(ctx, q, profileID)
	if err != nil {
		return nil, err
	}
	out := make([]StepWithDeadlines, 0, len(steps))
	for _, s := range steps {
		// Never nil: the client renders the list directly, and a JSON null
		// would make every consumer guard before iterating.
		lds := []LanguageDeadline{}
		for lang, ts := range byStep.Languages(s.ID) {
			lds = append(lds, LanguageDeadline{TargetLanguage: lang, Deadline: ts.Time.UTC().Format(time.RFC3339)})
		}
		sort.Slice(lds, func(i, j int) bool { return lds[i].TargetLanguage < lds[j].TargetLanguage })
		out = append(out, StepWithDeadlines{WorkflowStep: s, LanguageDeadlines: lds})
	}
	return out, nil
}

// resolvedLanguageDeadline is a validated LanguageDeadlineInput. Only entries
// that actually set a date survive validation — clearing one is expressed by
// leaving it out of the rewritten set.
type resolvedLanguageDeadline struct {
	lang     string
	deadline pgtype.Timestamptz
}

// resolveLanguageDeadlines validates one step's overrides: the step must be
// one a human works, every language must normalize to a non-empty code that
// the project actually translates into, and no language may appear twice.
// allowedLangs nil means "no list to check against" (a template profile).
// stepPos is 1-based, matching the surrounding step error messages.
func resolveLanguageDeadlines(stepPos int, stepType string, allowedLangs map[string]bool, in []LanguageDeadlineInput) ([]resolvedLanguageDeadline, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if stepdef.IsAutomated(stepType) {
		return nil, httputil.NewClientError(fmt.Sprintf("step %d: an automated step runs by itself and cannot have a deadline", stepPos))
	}
	out := make([]resolvedLanguageDeadline, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, ld := range in {
		lang := langcode.Normalize(ld.TargetLanguage)
		if lang == "" {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: a per-language deadline needs a target language", stepPos))
		}
		if seen[lang] {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: two deadlines for target language %q", stepPos, lang))
		}
		seen[lang] = true
		if allowedLangs != nil && !allowedLangs[lang] {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: %q is not one of this project's target languages", stepPos, lang))
		}
		deadline, ok := httputil.ParseDeadline(ld.Deadline)
		if !ok {
			return nil, httputil.NewClientError(fmt.Sprintf("step %d: invalid deadline format for %q (use ISO 8601)", stepPos, lang))
		}
		if !deadline.Valid {
			continue // cleared — simply absent from the rewritten set
		}
		out = append(out, resolvedLanguageDeadline{lang: lang, deadline: deadline})
	}
	return out, nil
}

// profileTargetLanguages returns the set a profile's deadlines may name, and
// whether the profile belongs to a project at all. A template profile does
// not, and carries no dates: "Save as template" sends the wizard's step draft
// verbatim, dates included, and a template has no screen to show them on and
// hands none to the projects it creates (CopyAssignments drops them).
// Dropping them here keeps the stored rows equal to what the product says.
func profileTargetLanguages(ctx context.Context, q db.Querier, profileID int64) (langs map[string]bool, isProject bool, err error) {
	proj, err := q.GetProjectForProfile(ctx, profileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get project for profile: %w", err)
	}
	out := make(map[string]bool, len(proj.TargetLanguages))
	for _, l := range proj.TargetLanguages {
		out[langcode.Normalize(l)] = true
	}
	return out, true, nil
}

// deadlineInputsChanged reports whether an edit changes anything a step's
// assignment deadlines resolve from: the step's own date, its per-language
// set, or its type (an ai step resolves to none). stored is the step's
// per-language rows before the write, as StepDeadlines.Languages returns them.
func deadlineInputsChanged(e db.WorkflowStep, r resolvedStep, stored map[string]pgtype.Timestamptz) bool {
	if e.StepType != r.stepType || !sameTimestamptz(e.Deadline, r.deadline) {
		return true
	}
	if len(stored) != len(r.langDeadlines) {
		return true
	}
	for _, ld := range r.langDeadlines {
		prev, ok := stored[ld.lang]
		if !ok || !sameTimestamptz(prev, ld.deadline) {
			return true
		}
	}
	return false
}

// sameTimestamptz reports whether two optional timestamps hold the same
// instant: both unset, or both set and equal regardless of zone.
func sameTimestamptz(a, b pgtype.Timestamptz) bool {
	if !a.Valid || !b.Valid {
		return a.Valid == b.Valid
	}
	return a.Time.Equal(b.Time)
}

// writeLanguageDeadlines rewrites one step's per-language rows as a set and
// pushes the result onto the jobs already sitting on that step. Call inside
// the same transaction as the step write.
//
// Wholesale delete + insert, so it will need revisiting once the table carries
// an assignee: a deadline edit must not drop a language's assignee with it.
func writeLanguageDeadlines(ctx context.Context, q db.Querier, tenantID, stepID int64, lds []resolvedLanguageDeadline) error {
	if err := q.DeleteWorkflowStepAssignments(ctx, stepID); err != nil {
		return fmt.Errorf("delete step language deadlines: %w", err)
	}
	for _, ld := range lds {
		if err := q.CreateWorkflowStepAssignment(ctx, db.CreateWorkflowStepAssignmentParams{
			WorkflowStepID: stepID, TargetLanguage: ld.lang, Deadline: ld.deadline,
		}); err != nil {
			return fmt.Errorf("create step language deadline: %w", err)
		}
	}
	if err := q.RefreshJobStepAssignmentDeadlines(ctx, db.RefreshJobStepAssignmentDeadlinesParams{
		WorkflowStepID: stepID, TenantID: tenantID,
	}); err != nil {
		return fmt.Errorf("refresh step assignment deadlines: %w", err)
	}
	return nil
}
