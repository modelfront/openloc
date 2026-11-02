package settings

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// StepLocks says which positions of a profile's workflow jobs have reached.
// Jobs walk the steps in order, so the reached steps form a prefix: every
// position up to LockedThrough keeps its step, and only the positions after
// it may be swapped, reordered or removed.
type StepLocks struct {
	// ReachedJobs counts, per workflow step id, the jobs that reached it.
	ReachedJobs map[int64]int
	// LockedThrough is the last locked position, or -1 when none is.
	LockedThrough int
}

// Locked reports whether position i keeps its step.
func (l StepLocks) Locked(i int) bool { return i <= l.LockedThrough }

// LoadStepLocks reads the reached steps of profileID; steps are its rows in
// order (ListSettingsWorkflowSteps).
func LoadStepLocks(ctx context.Context, q db.Querier, tenantID, profileID int64, steps []db.WorkflowStep) (StepLocks, error) {
	rows, err := q.ListReachedStepsForProfile(ctx, db.ListReachedStepsForProfileParams{TenantID: tenantID, SettingsProfileID: profileID})
	if err != nil {
		return StepLocks{}, fmt.Errorf("list reached steps: %w", err)
	}
	locks := StepLocks{ReachedJobs: make(map[int64]int, len(rows)), LockedThrough: -1}
	for _, r := range rows {
		locks.ReachedJobs[r.WorkflowStepID] = int(r.Jobs)
	}
	for i, s := range steps {
		if locks.ReachedJobs[s.ID] > 0 {
			locks.LockedThrough = i
		}
	}
	return locks, nil
}
