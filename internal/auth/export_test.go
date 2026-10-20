package auth

import (
	"context"

	db "openloc/internal/database/sqlcgen"
)

// EnsureDefaultWorkflowForTest exposes the per-org default-workflow seeder so
// the external auth_test package can assert its outcome without wiring the full
// signup HTTP flow.
func EnsureDefaultWorkflowForTest(ctx context.Context, q db.Querier, tenantID int64) error {
	return ensureDefaultWorkflow(ctx, q, tenantID)
}

// Test-only shims so the external auth_test package can drive the membership
// materialization seam (unexported) without a Google token exchange. Compiled
// only in test builds.

func (h *Handler) MaterializeMembershipsForTest(ctx context.Context, userID int64, email, inviteToken string) {
	h.materializeMemberships(ctx, userID, email, inviteToken)
}

func (h *Handler) HasJoinableOrgForTest(ctx context.Context, email, inviteToken string) bool {
	return h.hasJoinableOrg(ctx, email, inviteToken)
}
