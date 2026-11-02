package mcp

import (
	"context"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
)

// RecordDurableForTest exposes the unexported durable audit path to the
// external mcp_test package (which imports testutil and so can't live in
// package mcp — see the import cycle testutil→api→mcp).
func RecordDurableForTest(ctx context.Context, q db.Querier, tool string, args any, outcome, errCode string, started time.Time) error {
	return recordDurable(ctx, q, tool, args, outcome, errCode, started)
}

// SetAttachBatchBudgetForTest sets how long a batch attach keeps starting new
// files, and returns the function that puts the default back.
func SetAttachBatchBudgetForTest(d time.Duration) (restore func()) {
	old := attachBatchBudget
	attachBatchBudget = d
	return func() { attachBatchBudget = old }
}

// TierForTest exposes the tier a caller gets: whether they get the manager
// tools and the support team's tools.
func (s *Server) TierForTest(ctx context.Context, claims *auth.Claims) (manager, operator bool) {
	t := s.tierFor(ctx, claims)
	return t.manager, t.operator
}

// ConnectTierForTest opens an in-memory session against one prebuilt tier,
// whatever the claims in ctx say, so a test can reach a tool's call-time
// gates on a server that lists it.
func (s *Server) ConnectTierForTest(ctx context.Context, manager, operator bool) (*sdk.ClientSession, error) {
	srv := s.servers[tier{manager: manager, operator: operator}]
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		return nil, err
	}
	return sdk.NewClient(&sdk.Implementation{Name: "openloc-test", Version: "test"}, nil).Connect(ctx, ct, nil)
}
