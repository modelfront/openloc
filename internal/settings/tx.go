package settings

import (
	"context"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
)

// withTx is database.InTx for a fn that returns a value. A replace that fails
// mid-loop rolls back its leading DELETE (pinned in
// assignments_integration_test.go).
func withTx[T any](ctx context.Context, b database.TxBeginner, fn func(db.Querier) (T, error)) (T, error) {
	var out T
	err := database.InTx(ctx, b, func(_ pgx.Tx, q db.Querier) error {
		var err error
		out, err = fn(q)
		return err
	})
	return out, err
}
