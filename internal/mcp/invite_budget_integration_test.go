package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"openloc/internal/api"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/testutil"
)

// countingSender accepts every email and counts them.
type countingSender struct {
	mu    sync.Mutex
	count int
}

func (s *countingSender) SendEmail(context.Context, string, string, emaillayout.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	return nil
}

func (s *countingSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// TestIntegrationInviteEmailBudgetSharedWithREST: REST and MCP spend one
// per-person invitation email budget, so invites sent over REST leave an MCP
// invite over budget. The refusal is recorded as a rate limit, not as a bad
// argument the model should rewrite.
func TestIntegrationInviteEmailBudgetSharedWithREST(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &countingSender{}
	router := api.NewRouter(api.Deps{
		Queries:     db.New(pool),
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: sender,
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	tenant := testutil.SeedTenant(t, q, "BudgetCorp")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@budget.test", "pass", "owner")

	for i := range 20 {
		rec := testutil.DoRequestWithRole(t, router, http.MethodPost, "/api/invites",
			map[string]string{"email": fmt.Sprintf("rest-%d@budget.test", i), "role": "linguist"}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
	}

	session := connect(t, srv.URL, testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityWrite, time.Hour))
	isErr, text := confirmedCall(t, session, "openloc_invite_member", map[string]any{"email": "mcp@budget.test", "role": "linguist"}, nil)
	if !isErr || !strings.Contains(text, "too many invitation emails") {
		t.Fatalf("MCP invite after 20 REST invites: isError=%v %q, want the email budget refusal", isErr, text)
	}
	if got := sender.Count(); got != 20 {
		t.Errorf("sent %d emails, want 20", got)
	}

	// The audit row is written asynchronously.
	var code *string
	for deadline := time.Now().Add(2 * time.Second); code == nil && time.Now().Before(deadline); {
		rows, err := q.ListMCPToolCalls(context.Background(), db.ListMCPToolCallsParams{TenantID: tenant.ID, RowLimit: 10})
		if err != nil {
			t.Fatalf("list audit rows: %v", err)
		}
		for _, r := range rows {
			if r.Tool == "openloc_invite_member" && r.ErrorCode != nil {
				code = r.ErrorCode
			}
		}
		if code == nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if code == nil {
		t.Fatal("no audit row for the refused invite")
	}
	if *code != "rate_limited" {
		t.Errorf("audit error_code = %q, want rate_limited", *code)
	}
}
