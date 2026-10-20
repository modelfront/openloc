package database

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/logging"
)

// conflictTries is how many times RetryConflicts runs an attempt.
const conflictTries = 3

// IsConflict reports whether err is a deadlock (40P01) or a serialization
// failure (40001): Postgres rolled the transaction back and running it again
// can succeed.
func IsConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

// RetryConflicts runs attempt, and runs it again after a short random pause
// when it fails with a conflict (IsConflict), up to conflictTries times.
// attempt must run one whole transaction and set every result it returns on
// each run, so a run that rolled back leaves nothing behind.
func RetryConflicts(ctx context.Context, action string, attempt func() error) error {
	var err error
	for try := 1; try <= conflictTries; try++ {
		if err = attempt(); err == nil || !IsConflict(err) || try == conflictTries {
			return err
		}
		logging.Warn(ctx, err, "transaction conflict, running it again", action)
		pause := time.Duration(20+rand.IntN(60)) * time.Millisecond
		select {
		case <-ctx.Done():
			return err
		case <-time.After(pause):
		}
	}
	return err
}
