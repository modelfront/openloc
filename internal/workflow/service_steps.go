package workflow

// service_steps.go — the step actions behind the /api/jobs/{id}/workflow
// routes, shared by the HTTP handler and the MCP tools. Each method resolves
// the job tenant-scoped first (pgx.ErrNoRows = not in the tenant) and returns
// httputil Client/Forbidden errors for caller mistakes.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/projectevents"
	"openloc/internal/stepdef"
)

var errNoActiveStep = httputil.NewClientError("job has no active workflow step")

// Actor is the user performing a step action and their role.
type Actor struct {
	UserID int64
	Role   string
}

func (s *Service) tenantJob(ctx context.Context, tenantID, jobID int64) (db.Job, error) {
	return s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
}

// CanActOnStep is the hard lock on advance / reject: the
// step's assignee, or any manager (owner / admin / project_manager), may act.
// A linguist who is not the assignee may not — including on an unassigned
// step, which only a manager can push along.
func CanActOnStep(role string, assignedTo *int64, userID int64) bool {
	if assignedTo != nil && *assignedTo == userID {
		return true
	}
	return auth.IsManagerRole(role)
}

// ListJobSteps returns every step assignment of a job, in step order.
func (s *Service) ListJobSteps(ctx context.Context, tenantID, jobID int64) ([]db.ListJobStepAssignmentsRow, error) {
	if _, err := s.tenantJob(ctx, tenantID, jobID); err != nil {
		return nil, err
	}
	steps, err := s.queries.ListJobStepAssignments(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("list job steps: %w", err)
	}
	return steps, nil
}

// activeStepFor returns the job's active step after the actor check.
func (s *Service) activeStepFor(ctx context.Context, tenantID, jobID int64, actor Actor, verb string) (db.GetActiveStepForJobRow, error) {
	if _, err := s.tenantJob(ctx, tenantID, jobID); err != nil {
		return db.GetActiveStepForJobRow{}, err
	}
	active, err := s.queries.GetActiveStepForJob(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetActiveStepForJobRow{}, errNoActiveStep
	}
	if err != nil {
		return db.GetActiveStepForJobRow{}, fmt.Errorf("get active step: %w", err)
	}
	if !CanActOnStep(actor.Role, active.AssignedTo, actor.UserID) {
		return db.GetActiveStepForJobRow{}, httputil.NewForbiddenError(
			"only the assigned user or a manager can " + verb + " this step")
	}
	return active, nil
}

// ActiveStep returns the job's active step, or errNoActiveStep.
func (s *Service) ActiveStep(ctx context.Context, tenantID, jobID int64) (db.GetActiveStepForJobRow, error) {
	if _, err := s.tenantJob(ctx, tenantID, jobID); err != nil {
		return db.GetActiveStepForJobRow{}, err
	}
	active, err := s.queries.GetActiveStepForJob(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetActiveStepForJobRow{}, errNoActiveStep
	}
	return active, err
}

// MaxNoteLength caps a hand-off comment or a rejection reason, in characters.
// The dialogs cap their text boxes at the same number.
const MaxNoteLength = 4000

// cleanNote trims a note a person typed and refuses one the database or a
// screen cannot hold: longer than MaxNoteLength, or with a control character
// other than a line break or a tab (Postgres rejects a NUL byte, and it
// would do so only at the write that follows the step's own writes).
func cleanNote(what, text string) (string, error) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxNoteLength {
		return "", httputil.NewClientError(fmt.Sprintf("The %s is too long: at most %d characters.", what, MaxNoteLength))
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", httputil.NewClientError(fmt.Sprintf("The %s contains a character that cannot be stored.", what))
		}
	}
	return text, nil
}

// Advance completes the job's active step for a person (Complete step).
// comment is the hand-off note for whoever works the next step; blank means
// none.
func (s *Service) Advance(ctx context.Context, tenantID, jobID int64, actor Actor, comment string) (AdvanceResult, error) {
	c, err := cleanNote("comment", comment)
	if err != nil {
		return AdvanceResult{}, err
	}
	active, err := s.activeStepFor(ctx, tenantID, jobID, actor, "complete")
	if err != nil {
		return AdvanceResult{}, err
	}
	var note *string
	if c != "" {
		note = &c
	}
	return s.AdvanceJob(ctx, tenantID, jobID, active, &actor.UserID, note, false, nil)
}

// CheckAssignee verifies userID is an active member of the tenant,
// so a client-supplied id cannot assign work to a foreign user.
func CheckAssignee(ctx context.Context, q db.Querier, tenantID, userID int64) (db.User, error) {
	errNotMember := httputil.NewClientError("assignee must be an active member of this organization")
	if userID == 0 {
		return db.User{}, httputil.NewClientError("user_id is required")
	}
	if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: tenantID}); err != nil {
		return db.User{}, errNotMember
	}
	u, err := q.GetUser(ctx, userID)
	if err != nil || !u.IsActive {
		return db.User{}, errNotMember
	}
	return u, nil
}

// stepOfJob returns the step assignment after checking it belongs to the job.
func (s *Service) stepOfJob(ctx context.Context, tenantID, jobID, stepAssignmentID int64) (db.GetCurrentStepForJobRow, error) {
	if _, err := s.tenantJob(ctx, tenantID, jobID); err != nil {
		return db.GetCurrentStepForJobRow{}, err
	}
	step, err := s.queries.GetCurrentStepForJob(ctx, stepAssignmentID)
	if err != nil || step.JobID != jobID {
		return db.GetCurrentStepForJobRow{}, pgx.ErrNoRows
	}
	return step, nil
}

// AssignStep assigns a member to one of the job's step assignments.
func (s *Service) AssignStep(ctx context.Context, tenantID, jobID, stepAssignmentID, assigneeID int64) error {
	step, err := s.stepOfJob(ctx, tenantID, jobID, stepAssignmentID)
	if err != nil {
		return err
	}
	if stepdef.IsAutomated(step.StepType) {
		return httputil.NewClientError("an automated step runs by itself and cannot be assigned")
	}
	assignee, err := CheckAssignee(ctx, s.queries, tenantID, assigneeID)
	if err != nil {
		return err
	}
	if err := s.queries.AssignStepToUser(ctx, db.AssignStepToUserParams{
		ID:         stepAssignmentID,
		AssignedTo: &assigneeID,
	}); err != nil {
		return fmt.Errorf("assign step: %w", err)
	}
	s.emitStepEvent(ctx, tenantID, projectevents.TypeStepAssigned, jobID, map[string]any{
		"step": step.StepName, "assignee_id": assigneeID, "assignee_email": assignee.Email,
		"assignee_name": projectevents.PersonName(assignee),
	})
	return nil
}

// SetStepDeadline sets (or, with an invalid timestamp, clears) the deadline
// on one of the job's step assignments.
func (s *Service) SetStepDeadline(ctx context.Context, tenantID, jobID, stepAssignmentID int64, deadline pgtype.Timestamptz) error {
	step, err := s.stepOfJob(ctx, tenantID, jobID, stepAssignmentID)
	if err != nil {
		return err
	}
	// Same rule as the workflow editor (workflow_steps_ai_no_deadline).
	if deadline.Valid && stepdef.IsAutomated(step.StepType) {
		return httputil.NewClientError("an automated step runs by itself and cannot have a deadline")
	}
	if err := s.queries.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID:       stepAssignmentID,
		Deadline: deadline,
	}); err != nil {
		return fmt.Errorf("update deadline: %w", err)
	}
	if from, to := tsOrNil(step.Deadline), tsOrNil(deadline); from != to {
		s.emitStepEvent(ctx, tenantID, projectevents.TypeStepDeadlineSet, jobID, map[string]any{
			"step": step.StepName,
			"changes": map[string]projectevents.Change{
				"deadline": {From: from, To: to},
			},
		})
	}
	return nil
}
