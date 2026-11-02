package auth

import (
	"context"
)

// Test-only shims so the external auth_test package can drive the membership
// materialization seam (unexported) without a Google token exchange. Compiled
// only in test builds.

func (h *Handler) MaterializeMembershipsForTest(ctx context.Context, userID int64, email, inviteToken string) {
	h.materializeMemberships(ctx, userID, email, inviteToken)
}

func (h *Handler) HasJoinableOrgForTest(ctx context.Context, email, inviteToken string) bool {
	return h.hasJoinableOrg(ctx, email, inviteToken)
}
