package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"openloc/internal/auth"
)

func claimsCtx(tenantID, userID int64) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{TenantID: tenantID, UserID: userID, Role: auth.RoleOwner})
}

func TestConfirmTokenBinding(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := &Server{confirmKey: []byte("k"), now: func() time.Time { return now }}
	ctx := claimsCtx(1, 10)
	args := []byte(`{"id":"ocean"}`)
	tok := s.signConfirm(ctx, "tool_a", args, now.Add(confirmTTL))

	if !s.verifyConfirm(ctx, "tool_a", args, tok) {
		t.Fatal("a fresh token for the same call did not verify")
	}
	_, mac, _ := strings.Cut(tok, ".")
	cases := []struct {
		name  string
		ctx   context.Context
		tool  string
		args  []byte
		token string
	}{
		{"other tool", ctx, "tool_b", args, tok},
		{"other arguments", ctx, "tool_a", []byte(`{"id":"forest"}`), tok},
		{"other user, same tenant", claimsCtx(1, 11), "tool_a", args, tok},
		{"other tenant", claimsCtx(2, 10), "tool_a", args, tok},
		{"garbage", ctx, "tool_a", args, "not-a-token"},
		{"tampered expiry", ctx, "tool_a", args, "9999999999." + mac},
	}
	for _, c := range cases {
		if s.verifyConfirm(c.ctx, c.tool, c.args, c.token) {
			t.Errorf("%s: token verified but must not", c.name)
		}
	}

	now = now.Add(confirmTTL + time.Second)
	if s.verifyConfirm(ctx, "tool_a", args, tok) {
		t.Error("an expired token verified")
	}

	other := &Server{confirmKey: []byte("other-key"), now: s.now}
	now = now.Add(-confirmTTL)
	if other.verifyConfirm(ctx, "tool_a", args, tok) {
		t.Error("a token verified under a different key")
	}
}

func TestCanonicalArgsIgnoresToken(t *testing.T) {
	a, err := canonicalArgs(DeleteCustomPaletteIn{ID: "ocean"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalArgs(DeleteCustomPaletteIn{ID: "ocean", Confirm: Confirm{ConfirmToken: "x.y"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("token changed the canonical args: %s vs %s", a, b)
	}
	c, _ := canonicalArgs(DeleteCustomPaletteIn{ID: "forest"})
	if string(a) == string(c) {
		t.Error("different arguments gave the same canonical args")
	}
}
