package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func membershipRole(t *testing.T, f *fixture, userID, tenantID int64) string {
	t.Helper()
	m, err := f.q.GetMembership(context.Background(), db.GetMembershipParams{UserID: userID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("get membership: %v", err)
	}
	return m.Role
}

func TestIntegrationMCPInsightsGetMeForLinguist(t *testing.T) {
	f := newFixture(t)
	if err := f.q.InsertUserLanguagePair(context.Background(), db.InsertUserLanguagePairParams{
		UserID: f.linguistA.ID, SourceLang: "en", TargetLang: "de", TenantID: f.tenantA.ID,
	}); err != nil {
		t.Fatalf("seed pair: %v", err)
	}
	var me struct {
		UserID        int64  `json:"user_id"`
		Role          string `json:"role"`
		TenantName    string `json:"tenant_name"`
		LanguagePairs []struct {
			SourceLang string `json:"source_lang"`
			TargetLang string `json:"target_lang"`
		} `json:"language_pairs"`
	}
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_get_me", map[string]any{}, &me); isErr {
		t.Fatalf("get_me: %s", text)
	}
	if me.UserID != f.linguistA.ID || me.Role != "linguist" || me.TenantName != f.tenantA.Name {
		t.Errorf("me = %+v", me)
	}
	if len(me.LanguagePairs) != 1 || me.LanguagePairs[0].SourceLang != "en" || me.LanguagePairs[0].TargetLang != "de" {
		t.Errorf("pairs = %+v", me.LanguagePairs)
	}
}

func TestIntegrationMCPInsightsInviteRules(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-inv@test.com", "pass", "project_manager")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))

	// A project manager only invites linguists, and owner is never invited —
	// same rules as POST /api/invites. The preview refuses, so no confirm card.
	for _, tc := range []struct{ email, role, want string }{
		{"boss@example.com", "admin", "project managers can only invite linguists"},
		{"chief@example.com", "owner", "role must be"},
	} {
		var g guardedOut
		isErr, text := callTool(t, s, "openloc_invite_member", map[string]any{"email": tc.email, "role": tc.role}, &g)
		if !isErr || !strings.Contains(text, tc.want) || g.Status == "needs_confirmation" {
			t.Fatalf("pm invite as %s: preview should refuse, got %v %s %+v", tc.role, isErr, text, g)
		}
		if _, err := f.q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
			Email: tc.email, TenantID: f.tenantA.ID,
		}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("refused invite as %s was stored anyway: %v", tc.role, err)
		}
	}

	// The first call only previews: no invite row, no email.
	var preview guardedOut
	if isErr, text := callTool(t, s, "openloc_invite_member", map[string]any{"email": " Vendor@Example.com "}, &preview); isErr {
		t.Fatalf("preview invite: %s", text)
	}
	if preview.Status != "needs_confirmation" || preview.ConfirmToken == "" || preview.Preview == nil ||
		!strings.Contains(preview.Preview.Summary, "vendor@example.com") || !strings.Contains(preview.Preview.Summary, "linguist") {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := f.q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
		Email: "vendor@example.com", TenantID: f.tenantA.ID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("preview created the invite: %v", err)
	}

	var inv struct {
		ID     int64  `json:"id"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_invite_member", map[string]any{"email": " Vendor@Example.com "}, &inv); isErr {
		t.Fatalf("invite linguist: %s", text)
	}
	stored, err := f.q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
		Email: "vendor@example.com", TenantID: f.tenantA.ID,
	})
	if err != nil || stored.ID != inv.ID || stored.Role != "linguist" || inv.Status != "pending" {
		t.Fatalf("stored invite = %+v, tool = %+v, err %v", stored, inv, err)
	}

	// Duplicate pending invite is a caller mistake.
	if isErr, text := confirmedCall(t, s, "openloc_invite_member", map[string]any{"email": "vendor@example.com"}, nil); !isErr || !strings.Contains(text, "already exists") {
		t.Errorf("duplicate invite: %v %s", isErr, text)
	}

	// The list never carries the invite link token.
	res, text := callTool(t, s, "openloc_list_invites", map[string]any{}, nil)
	if res {
		t.Fatalf("list_invites: %s", text)
	}
	if !strings.Contains(text, "vendor@example.com") || strings.Contains(text, stored.Token) {
		t.Errorf("list_invites leaks the token or misses the invite: %s", text)
	}

	// Read-only token cannot invite.
	readPM := connect(t, f.url, testutil.SeedAPIToken(t, f.q, f.tenantA.ID, pm.ID, auth.CapabilityRead, time.Hour))
	if isErr, text := callTool(t, readPM, "openloc_invite_member", map[string]any{"email": "x@example.com"}, nil); !isErr || !strings.Contains(text, "write access") {
		t.Errorf("read-only invite: %v %s", isErr, text)
	}

	// Revoke: preview keeps the row, confirm removes it; tenant B can't see it.
	sB := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	if isErr, text := callTool(t, sB, "openloc_revoke_invite", map[string]any{"invite_id": inv.ID}, nil); !isErr {
		t.Fatal("tenant B previewed tenant A's invite")
	} else {
		assertOpaqueNotFound(t, text)
	}
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_revoke_invite", map[string]any{"invite_id": inv.ID}, &g); isErr {
		t.Fatalf("preview revoke: %s", text)
	}
	if !strings.Contains(g.Preview.Summary, "vendor@example.com") || len(g.Preview.Effects) == 0 {
		t.Errorf("preview = %+v", g.Preview)
	}
	if _, err := f.q.GetInviteTenant(context.Background(), db.GetInviteTenantParams{ID: inv.ID, TenantID: f.tenantA.ID}); err != nil {
		t.Fatalf("preview revoked the invite: %v", err)
	}
	if isErr, text := confirmedCall(t, s, "openloc_revoke_invite", map[string]any{"invite_id": inv.ID}, nil); isErr {
		t.Fatalf("confirm revoke: %s", text)
	}
	if _, err := f.q.GetInviteTenant(context.Background(), db.GetInviteTenantParams{ID: inv.ID, TenantID: f.tenantA.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("invite still there after revoke: %v", err)
	}
}

// TestIntegrationMCPInsightsInviteScope: a project manager's invite tools reach
// only linguist invites, the same rule as GET and DELETE /api/invites.
func TestIntegrationMCPInsightsInviteScope(t *testing.T) {
	f := newFixture(t)
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-scope@test.com", "pass", "project_manager")
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	seed := func(email, role string) db.Invite {
		t.Helper()
		token, err := testutil.RandomToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		inv, err := f.q.CreateInvite(context.Background(), db.CreateInviteParams{
			TenantID: f.tenantA.ID, Email: email, Role: role, InvitedBy: f.ownerA.ID, Token: token,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		if err != nil {
			t.Fatalf("seed invite: %v", err)
		}
		return inv
	}
	adminInv := seed("boss@example.com", "admin")
	seed("vendor@example.com", "linguist")

	isErr, text := callTool(t, s, "openloc_list_invites", map[string]any{}, nil)
	if isErr {
		t.Fatalf("list_invites: %s", text)
	}
	if strings.Contains(text, "boss@example.com") || !strings.Contains(text, "vendor@example.com") {
		t.Errorf("a project manager's list must hold only linguist invites: %s", text)
	}

	// The confirm step needs the preview's token, so a refused preview is the
	// whole gate here; the REST tests pin Revoke's own check.
	if isErr, text := callTool(t, s, "openloc_revoke_invite", map[string]any{"invite_id": adminInv.ID}, nil); !isErr {
		t.Fatal("a project manager previewed revoking an admin invite")
	} else {
		assertOpaqueNotFound(t, text)
	}
	if _, err := f.q.GetInviteTenant(context.Background(), db.GetInviteTenantParams{ID: adminInv.ID, TenantID: f.tenantA.ID}); err != nil {
		t.Fatalf("admin invite is gone: %v", err)
	}

	// An admin doesn't reach an owner invite (they come from platform operators).
	admin := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "admin-scope@test.com", "pass", "admin")
	sAdmin := connect(t, f.url, writeToken(t, f, f.tenantA.ID, admin.ID))
	ownerInv := seed("future-owner@example.com", "owner")
	if isErr, text := callTool(t, sAdmin, "openloc_list_invites", map[string]any{}, nil); isErr {
		t.Fatalf("admin list_invites: %s", text)
	} else if strings.Contains(text, "future-owner@example.com") || !strings.Contains(text, "boss@example.com") {
		t.Errorf("an admin's list must hold every invite but owner invites: %s", text)
	}
	if isErr, text := callTool(t, sAdmin, "openloc_revoke_invite", map[string]any{"invite_id": ownerInv.ID}, nil); !isErr {
		t.Fatal("an admin previewed revoking an owner invite")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPInsightsChangeMemberRole(t *testing.T) {
	f := newFixture(t)
	admin := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "admin-role@test.com", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-role@test.com", "pass", "project_manager")
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	// Owner/admin only, like PUT /api/members/{id}/role.
	pmSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
	if isErr, text := callTool(t, pmSession, "openloc_change_member_role", map[string]any{"user_id": f.linguistA.ID, "role": "admin"}, nil); !isErr || !strings.Contains(text, "only an owner or admin") {
		t.Fatalf("pm changed a role: %v %s", isErr, text)
	}

	// Preview names the change and changes nothing.
	var g guardedOut
	if isErr, text := callTool(t, owner, "openloc_change_member_role", map[string]any{"user_id": f.linguistA.ID, "role": "project_manager"}, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if !strings.Contains(g.Preview.Summary, "from linguist to project_manager") {
		t.Errorf("preview summary = %q", g.Preview.Summary)
	}
	if got := membershipRole(t, f, f.linguistA.ID, f.tenantA.ID); got != "linguist" {
		t.Fatalf("preview changed the role to %q", got)
	}
	var changed struct {
		PreviousRole string `json:"previous_role"`
		Role         string `json:"role"`
	}
	if isErr, text := confirmedCall(t, owner, "openloc_change_member_role", map[string]any{"user_id": f.linguistA.ID, "role": "project_manager"}, &changed); isErr {
		t.Fatalf("confirm: %s", text)
	}
	if got := membershipRole(t, f, f.linguistA.ID, f.tenantA.ID); got != "project_manager" || changed.PreviousRole != "linguist" {
		t.Errorf("role after confirm = %q (previous %q)", got, changed.PreviousRole)
	}

	// Role hierarchy: an admin cannot touch the owner, nobody changes themselves.
	adminSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, admin.ID))
	if isErr, text := callTool(t, adminSession, "openloc_change_member_role", map[string]any{"user_id": f.ownerA.ID, "role": "linguist"}, nil); !isErr || !strings.Contains(text, "only an owner can change another owner") {
		t.Errorf("admin demoted the owner: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, adminSession, "openloc_change_member_role", map[string]any{"user_id": pm.ID, "role": "owner"}, nil); !isErr || !strings.Contains(text, "only an owner can grant owner") {
		t.Errorf("admin granted owner: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, owner, "openloc_change_member_role", map[string]any{"user_id": f.ownerA.ID, "role": "admin"}, nil); !isErr || !strings.Contains(text, "your own role") {
		t.Errorf("owner changed own role: %v %s", isErr, text)
	}
	if got := membershipRole(t, f, f.ownerA.ID, f.tenantA.ID); got != "owner" {
		t.Errorf("owner's role is now %q", got)
	}

	// A member of another org is opaque not-found, whatever the role says.
	if isErr, text := callTool(t, owner, "openloc_change_member_role", map[string]any{"user_id": f.ownerB.ID, "role": "bogus"}, nil); !isErr {
		t.Fatal("changed a foreign member")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPInsightsRemoveMember(t *testing.T) {
	f := newFixture(t)
	admin := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "admin-rm@test.com", "pass", "admin")
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	adminSession := connect(t, f.url, writeToken(t, f, f.tenantA.ID, admin.ID))
	if isErr, text := callTool(t, adminSession, "openloc_remove_member", map[string]any{"user_id": f.ownerA.ID}, nil); !isErr || !strings.Contains(text, "only an owner can remove another owner") {
		t.Fatalf("admin removed the owner: %v %s", isErr, text)
	}

	var g guardedOut
	if isErr, text := callTool(t, owner, "openloc_remove_member", map[string]any{"user_id": f.linguistA.ID}, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if !strings.Contains(g.Preview.Summary, f.linguistA.Name) {
		t.Errorf("preview summary = %q", g.Preview.Summary)
	}
	if membershipRole(t, f, f.linguistA.ID, f.tenantA.ID) == "" {
		t.Fatal("preview removed the member")
	}
	if isErr, text := confirmedCall(t, owner, "openloc_remove_member", map[string]any{"user_id": f.linguistA.ID}, nil); isErr {
		t.Fatalf("confirm: %s", text)
	}
	if got := membershipRole(t, f, f.linguistA.ID, f.tenantA.ID); got != "" {
		t.Errorf("membership survived removal: %q", got)
	}
	if _, err := f.q.GetUser(context.Background(), f.linguistA.ID); err != nil {
		t.Errorf("removal deleted the account itself: %v", err)
	}

	if isErr, text := callTool(t, owner, "openloc_remove_member", map[string]any{"user_id": f.ownerB.ID}, nil); !isErr {
		t.Fatal("removed a foreign member")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPInsightsSetMemberLanguagePairs(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out struct {
		Pairs []struct {
			SourceLang string `json:"source_lang"`
			TargetLang string `json:"target_lang"`
		} `json:"pairs"`
	}
	if isErr, text := callTool(t, owner, "openloc_set_member_language_pairs", map[string]any{
		"user_id": f.linguistA.ID,
		"pairs":   []map[string]string{{"source_lang": "EN", "target_lang": "de-de"}, {"source_lang": "en", "target_lang": "DE-DE"}},
	}, &out); isErr {
		t.Fatalf("set pairs: %s", text)
	}
	stored, err := f.q.ListUserLanguagePairs(context.Background(), db.ListUserLanguagePairsParams{UserID: f.linguistA.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatalf("list pairs: %v", err)
	}
	if len(stored) != 1 || stored[0].SourceLang != "en" || stored[0].TargetLang != "de-de" || len(out.Pairs) != 1 {
		t.Errorf("stored pairs = %+v, tool = %+v (want one normalized en→de-de)", stored, out.Pairs)
	}

	if isErr, text := callTool(t, owner, "openloc_set_member_language_pairs", map[string]any{
		"user_id": f.linguistA.ID, "pairs": []map[string]string{{"source_lang": "en", "target_lang": "en"}},
	}, nil); !isErr || !strings.Contains(text, "must differ") {
		t.Errorf("same-language pair accepted: %v %s", isErr, text)
	}

	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm-pairs@test.com", "pass", "project_manager")
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID)), "openloc_set_member_language_pairs", map[string]any{
		"user_id": f.linguistA.ID, "pairs": []map[string]string{},
	}, nil); !isErr || !strings.Contains(text, "only an owner or admin") {
		t.Errorf("pm replaced pairs: %v %s", isErr, text)
	}

	if isErr, text := callTool(t, owner, "openloc_set_member_language_pairs", map[string]any{
		"user_id": f.ownerB.ID, "pairs": []map[string]string{},
	}, nil); !isErr {
		t.Fatal("replaced a foreign user's pairs")
	} else {
		assertOpaqueNotFound(t, text)
	}
}
