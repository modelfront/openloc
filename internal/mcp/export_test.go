package mcp

import (
	"context"
	"time"

	db "openloc/internal/database/sqlcgen"
)

// RecordDurableForTest exposes the unexported durable audit path to the
// external mcp_test package (which imports testutil and so can't live in
// package mcp — see the import cycle testutil→api→mcp).
func RecordDurableForTest(ctx context.Context, q db.Querier, tool string, args any, outcome, errCode string, started time.Time) error {
	return recordDurable(ctx, q, tool, args, outcome, errCode, started)
}
