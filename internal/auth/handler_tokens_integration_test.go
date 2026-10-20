// Personal access token lifecycle: creation, the show-once rule,
// revocation, expiry, cross-tenant isolation, and the per-request role
// resolution that distinguishes PATs from browser JWTs.
package auth_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type tokenListResp struct {
	Data []struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Prefix     string  `json:"prefix"`
		Capability string  `json:"capability"`
		RevokedAt  *string `json:"revoked_at"`
		UserEmail  string  `json:"user_email"`
	} `json:"data"`
}

// createTokenResp mirrors the {"data": …} envelope httputil.Created writes.
type createTokenResp struct {
	Data struct {
		Token    string `json:"token"`
		APIToken struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Prefix     string `json:"prefix"`
			Capability string `json:"capability"`
		} `json:"api_token"`
	} `json:"data"`
}

func TestIntegrationAPITokenCreateAndUse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "TokenOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-tok@test.com", "pass", "owner")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/api-tokens",
		map[string]any{"name": "claude-desktop"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var created createTokenResp
	testutil.DecodeJSON(t, rec, &created)
	if !auth.IsPAT(created.Data.Token) {
		t.Fatalf("created token is not a PAT: %q", created.Data.Token)
	}
	if created.Data.APIToken.Capability != auth.CapabilityRead {
		t.Errorf("default capability = %q, want read", created.Data.APIToken.Capability)
	}

	// The token must actually authenticate a normal API request — the whole point
	// is that it's a first-class credential, not a special case for /mcp.
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, created.Data.Token)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Show-once: the secret must never appear in any subsequent read.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, created.Data.Token) {
		t.Fatal("token list leaked the plaintext token")
	}
	if strings.Contains(body, "token_hash") {
		t.Fatal("token list leaked the token hash")
	}
	var list tokenListResp
	testutil.DecodeJSON(t, rec, &list)
	if len(list.Data) != 1 || list.Data[0].Name != "claude-desktop" {
		t.Fatalf("unexpected token list: %+v", list.Data)
	}
	// The display prefix is safe to show, and must be a strict prefix.
	if !strings.HasPrefix(created.Data.Token, list.Data[0].Prefix) {
		t.Errorf("listed prefix %q does not prefix the real token", list.Data[0].Prefix)
	}
}

func TestIntegrationAPITokenValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "ValidateOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-val@test.com", "pass", "owner")

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing name", map[string]any{}, http.StatusBadRequest},
		{"blank name", map[string]any{"name": "   "}, http.StatusBadRequest},
		{"name too long", map[string]any{"name": strings.Repeat("x", 101)}, http.StatusBadRequest},
		{"bad capability", map[string]any{"name": "t", "capability": "admin"}, http.StatusBadRequest},
		{"zero expiry", map[string]any{"name": "t", "expires_in_days": 0}, http.StatusBadRequest},
		{"negative expiry", map[string]any{"name": "t", "expires_in_days": -5}, http.StatusBadRequest},
		{"expiry beyond max", map[string]any{"name": "t", "expires_in_days": 400}, http.StatusBadRequest},
		{"valid write token", map[string]any{"name": "t", "capability": "write", "expires_in_days": 30}, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testutil.DoRequestWithRole(t, router, "POST", "/api/api-tokens", tc.body, owner.ID, tenant.ID, "owner")
			testutil.AssertStatus(t, rec, tc.want)
		})
	}
}

func TestIntegrationAPITokenRevokedAndExpired(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RevokeOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner-rev@test.com", "pass", "owner")

	// An already-expired token must not authenticate.
	expired := testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityRead, -time.Hour)
	rec := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, expired)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)

	// A live token works, then stops working the moment it's revoked.
	live := testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityRead, time.Hour)
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, live)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens", nil, owner.ID, tenant.ID, "owner")
	var list tokenListResp
	testutil.DecodeJSON(t, rec, &list)
	var liveID int64
	for _, tok := range list.Data {
		if strings.HasPrefix(live, tok.Prefix) {
			liveID = tok.ID
		}
	}
	if liveID == 0 {
		t.Fatal("could not find the live token in the listing")
	}

	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		"/api/api-tokens/"+itoa(liveID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, live)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)

	// Revocation is idempotent — a second DELETE must not 500 or 404.
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		"/api/api-tokens/"+itoa(liveID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

func TestIntegrationAPITokenCrossTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "TokTenantA")
	tenantB := testutil.SeedTenant(t, q, "TokTenantB")
	ownerA := testutil.SeedUserWithRole(t, q, tenantA.ID, "a-tok@test.com", "pass", "owner")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "b-tok@test.com", "pass", "owner")

	projA := testutil.SeedProject(t, q, tenantA.ID, "A Project", "en", []string{"de"})
	tokenB := testutil.SeedAPIToken(t, q, tenantB.ID, ownerB.ID, auth.CapabilityRead, time.Hour)

	// Tenant B's token must not reach tenant A's project.
	rec := testutil.DoRequestWithRawToken(t, router, "GET",
		"/api/projects/"+itoa(projA.ID), nil, tokenB)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// ...nor see it in a listing.
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, tokenB)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "A Project") {
		t.Fatal("tenant B's token enumerated tenant A's project")
	}

	// Tenant A's owner must not be able to revoke tenant B's token. 404 (not 403)
	// so the response can't confirm that the id exists in another org.
	tokenARec := testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens?all=true", nil, ownerB.ID, tenantB.ID, "owner")
	var listB tokenListResp
	testutil.DecodeJSON(t, tokenARec, &listB)
	if len(listB.Data) == 0 {
		t.Fatal("expected tenant B to have a token")
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		"/api/api-tokens/"+itoa(listB.Data[0].ID), nil, ownerA.ID, tenantA.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// And B's token still works, proving the failed cross-tenant revoke was a
	// genuine no-op rather than a 404 masking a successful write.
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, tokenB)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func TestIntegrationAPITokenRoleResolvedPerRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "RoleOrg")
	// A second owner so the last-admin guard doesn't block the demotion below.
	testutil.SeedUserWithRole(t, q, tenant.ID, "keeper@test.com", "pass", "owner")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "demoted@test.com", "pass", "admin")
	token := testutil.SeedAPIToken(t, q, tenant.ID, user.ID, auth.CapabilityRead, time.Hour)
	ctx := context.Background()

	// As an admin, the token reaches a manager-gated route.
	rec := testutil.DoRequestWithRawToken(t, router, "GET", "/api/members", nil, token)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Demote to linguist. The token is untouched — only the membership changes.
	if err := q.UpdateMembershipRole(ctx, db.UpdateMembershipRoleParams{
		UserID: user.ID, TenantID: tenant.ID, Role: "linguist",
	}); err != nil {
		t.Fatalf("demote: %v", err)
	}

	// This is the behaviour that distinguishes PATs from browser JWTs: the role
	// is read from memberships on every request, so the demotion lands
	// immediately rather than after the token expires.
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/members", nil, token)
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Removing the membership entirely kills the token — the auth query's JOIN
	// against memberships yields no row.
	if err := q.DeleteMembership(ctx, db.DeleteMembershipParams{
		UserID: user.ID, TenantID: tenant.ID,
	}); err != nil {
		t.Fatalf("delete membership: %v", err)
	}
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, token)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)
}

func TestIntegrationAPITokenDeactivatedUser(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "DeactOrg")
	testutil.SeedUserWithRole(t, q, tenant.ID, "keeper-d@test.com", "pass", "owner")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "leaver@test.com", "pass", "admin")
	token := testutil.SeedAPIToken(t, q, tenant.ID, user.ID, auth.CapabilityRead, time.Hour)

	rec := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, token)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Deactivating the identity must invalidate its tokens — otherwise offboarding
	// a person leaves their agents running.
	if err := q.DeactivateUser(context.Background(), user.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, token)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)
}

func TestIntegrationAPITokenOwnershipAndManagerScope(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "OwnOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "own-o@test.com", "pass", "owner")
	ling1 := testutil.SeedUserWithRole(t, q, tenant.ID, "l1@test.com", "pass", "linguist")
	ling2 := testutil.SeedUserWithRole(t, q, tenant.ID, "l2@test.com", "pass", "linguist")

	// A linguist may create their own token — they need one to drive the CAT
	// editor tools from an agent.
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/api-tokens",
		map[string]any{"name": "ling1-agent"}, ling1.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created createTokenResp
	testutil.DecodeJSON(t, rec, &created)

	// A linguist must not be able to enumerate the org's tokens.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens?all=true", nil, ling1.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Their own listing shows only their own token.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens", nil, ling2.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var l2 tokenListResp
	testutil.DecodeJSON(t, rec, &l2)
	if len(l2.Data) != 0 {
		t.Errorf("linguist 2 sees %d tokens, want 0 (they own none)", len(l2.Data))
	}

	// One linguist must not revoke another user's token.
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		"/api/api-tokens/"+itoa(created.Data.APIToken.ID), nil, ling2.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// A manager may — needed to cut off a departing teammate's agents.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/api-tokens?all=true", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var all tokenListResp
	testutil.DecodeJSON(t, rec, &all)
	if len(all.Data) != 1 || all.Data[0].UserEmail != "l1@test.com" {
		t.Fatalf("manager listing = %+v, want linguist 1's token with its owner email", all.Data)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE",
		"/api/api-tokens/"+itoa(created.Data.APIToken.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, created.Data.Token)
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)
}

func TestIntegrationMalformedPATDoesNotFallThrough(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "FallthroughOrg")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "ft@test.com", "pass", "owner")

	// A value carrying the PAT prefix is a PAT claim. It must be rejected as a
	// PAT, never retried against the JWT verifier — otherwise the two credential
	// paths stop being exclusive and the failure mode becomes hard to reason
	// about.
	rec := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, auth.PATPrefix+"garbage")
	testutil.AssertStatus(t, rec, http.StatusUnauthorized)

	// And a real JWT still works — the PAT branch must not shadow the JWT
	// path at the one shared chokepoint.
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/projects", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// A token whose hash exists but whose expiry has passed must be
	// indistinguishable from a token that never existed.
	expired := testutil.SeedAPIToken(t, q, tenant.ID, owner.ID, auth.CapabilityRead, -time.Minute)
	recExpired := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, expired)
	recUnknown := testutil.DoRequestWithRawToken(t, router, "GET", "/api/projects", nil, auth.PATPrefix+"nonexistent")
	if recExpired.Body.String() != recUnknown.Body.String() {
		t.Errorf("expired and unknown tokens must return identical bodies; got %q vs %q",
			recExpired.Body.String(), recUnknown.Body.String())
	}
}

// itoa keeps the URL building above readable.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
