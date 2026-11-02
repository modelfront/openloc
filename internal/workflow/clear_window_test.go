package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/workflow"
)

// stepQuerier answers GetCurrentStepForJob only; any other call panics on the
// nil embedded interface.
type stepQuerier struct {
	db.Querier
	row db.GetCurrentStepForJobRow
	err error
}

func (q stepQuerier) GetCurrentStepForJob(context.Context, int64) (db.GetCurrentStepForJobRow, error) {
	return q.row, q.err
}

func TestJobInClearWindow(t *testing.T) {
	stepID := int64(7)
	onStep := db.Job{ID: 1, CurrentStepID: &stepID}
	cases := []struct {
		name    string
		job     db.Job
		q       stepQuerier
		want    bool
		wantErr bool
	}{
		{"no current step (finished or no workflow)", db.Job{ID: 1}, stepQuerier{}, true, false},
		{"at the first human or hybrid step", onStep, stepQuerier{row: db.GetCurrentStepForJobRow{StepOrder: 2, LastClearStepOrder: 2}}, true, false},
		{"past it", onStep, stepQuerier{row: db.GetCurrentStepForJobRow{StepOrder: 3, LastClearStepOrder: 2}}, false, false},
		{"missing step row counts as in the window", onStep, stepQuerier{err: pgx.ErrNoRows}, true, false},
		{"any other lookup error is returned, never allowed", onStep, stepQuerier{err: errors.New("connection reset")}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := workflow.JobInClearWindow(context.Background(), c.q, c.job)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("JobInClearWindow = %v, want %v", got, c.want)
			}
		})
	}
}
