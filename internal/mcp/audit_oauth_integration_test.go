package mcp_test

// Pins that an OAuth-delegated MCP call audits with token_id = NULL: it
// carries PATInfo with no api_tokens row, and 0 would violate the FK to
// api_tokens and lose the row — silently for reads, as "applied but not
// recorded" for write tools.

import (
	"context"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

func TestIntegrationAuditOAuthTokenWritesNullTokenID(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	tenant := testutil.SeedTenant(t, q, "AuditOAuthTenant")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "audit-oauth@test.com", "pass", "owner")

	// The context an OAuth-authenticated request carries: claims + a PATInfo
	// whose Kind is OAuth and whose TokenID is 0 (no api_tokens row).
	ctx := auth.WithPATInfo(
		auth.WithClaims(context.Background(), &auth.Claims{TenantID: tenant.ID, UserID: user.ID, Role: "owner"}),
		&auth.PATInfo{Kind: auth.CredentialOAuth, Capability: auth.CapabilityWrite, RateLimitKey: -7, ClientName: "Claude"},
	)

	// A token_id of 0 here returns the FK-violation error instead of nil.
	if err := mcp.RecordDurableForTest(ctx, q, "openloc_save_draft", map[string]any{"x": 1}, "ok", "", time.Now()); err != nil {
		t.Fatalf("OAuth audit insert failed (token_id FK violation?): %v", err)
	}

	rows, err := q.ListMCPToolCalls(context.Background(), db.ListMCPToolCallsParams{
		TenantID: tenant.ID, Cursor: 0, RowLimit: 10,
	})
	if err != nil {
		t.Fatalf("list audit rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].TokenID != nil {
		t.Errorf("OAuth audit row token_id = %v, want NULL", *rows[0].TokenID)
	}
}
