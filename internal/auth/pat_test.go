package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

func TestGeneratePATFormat(t *testing.T) {
	raw, hash, prefix, err := GeneratePAT()
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}
	if !strings.HasPrefix(raw, PATPrefix) {
		t.Errorf("raw token missing prefix: %q", raw)
	}
	if !IsPAT(raw) {
		t.Error("IsPAT should accept a freshly generated token")
	}
	// The stored prefix must be a strict prefix of the raw token — it's a display
	// fragment, so if it ever diverged the UI would show a token that doesn't
	// exist.
	if !strings.HasPrefix(raw, prefix) {
		t.Errorf("display prefix %q is not a prefix of raw token %q", prefix, raw)
	}
	// The display fragment must not be enough to reconstruct the secret.
	if len(prefix) >= len(raw) {
		t.Errorf("display prefix leaks the whole token: prefix=%d raw=%d", len(prefix), len(raw))
	}
	if hash == raw {
		t.Fatal("hash must not equal the plaintext token")
	}
	if sum, err := hex.DecodeString(hash); err != nil || len(sum) != 32 {
		t.Errorf("expected a hex SHA-256 (32 bytes), got %q (err %v)", hash, err)
	}
	// The plaintext must not be recoverable from the stored hash. Weak check, but
	// it catches the catastrophic "stored the token in the hash column" slip.
	if strings.Contains(hash, strings.TrimPrefix(raw, PATPrefix)) {
		t.Error("hash contains the plaintext secret")
	}
}

func TestGeneratePATUniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		raw, _, _, err := GeneratePAT()
		if err != nil {
			t.Fatalf("GeneratePAT: %v", err)
		}
		if seen[raw] {
			t.Fatalf("GeneratePAT produced a duplicate on iteration %d", i)
		}
		seen[raw] = true
	}
}

func TestHashPATDeterministic(t *testing.T) {
	raw, hash, _, err := GeneratePAT()
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}
	if HashPAT(raw) != hash {
		t.Error("HashPAT is not deterministic for the same input")
	}
	if HashPAT(raw+"x") == hash {
		t.Error("HashPAT collided for different inputs")
	}
	// Hashing the FULL raw value (prefix included) means a hash can only ever
	// match a correctly-prefixed presentation. If we hashed only the secret part,
	// a token presented without its prefix would still validate.
	if HashPAT(strings.TrimPrefix(raw, PATPrefix)) == hash {
		t.Error("hash must cover the prefix, not just the secret")
	}
}

func TestIsPATRejectsOtherCredentials(t *testing.T) {
	// A JWT must never be mistaken for a PAT: the two verifiers are exclusive, so
	// a misclassification would send a valid JWT down the PAT path and 401 it.
	jwt, err := GenerateToken(1, 2, RoleOwner, "secret", time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"jwt", jwt},
		{"empty", ""},
		{"random", "abc123"},
		{"near-miss prefix", "eltm_pa_abc"},
		{"prefix in the middle", "xx" + PATPrefix + "abc"},
	} {
		if IsPAT(tc.token) {
			t.Errorf("IsPAT(%s) should be false", tc.name)
		}
	}
}

// --- Resolver -----------------------------------------------------------------

type stubTokenQuerier struct {
	row      db.GetAPITokenAuthRow
	err      error
	touched  int
	touchErr error
	gotHash  string
}

func (s *stubTokenQuerier) GetAPITokenAuth(_ context.Context, hash string) (db.GetAPITokenAuthRow, error) {
	s.gotHash = hash
	return s.row, s.err
}

func (s *stubTokenQuerier) TouchAPIToken(_ context.Context, _ db.TouchAPITokenParams) error {
	s.touched++
	return s.touchErr
}

func TestPATResolverSuccess(t *testing.T) {
	stub := &stubTokenQuerier{row: db.GetAPITokenAuthRow{
		TokenID:    7,
		TenantID:   42,
		UserID:     9,
		Capability: CapabilityRead,
		Role:       RoleProjectManager,
		UserEmail:  "pat-owner@x.test",
	}}
	resolve := NewPATResolver(stub)

	raw, hash, _, err := GeneratePAT()
	if err != nil {
		t.Fatalf("GeneratePAT: %v", err)
	}
	claims, info, err := resolve(context.Background(), raw)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// The resolver must look the token up by HASH, never by plaintext.
	if stub.gotHash != hash {
		t.Errorf("resolver looked up %q, want the SHA-256 %q", stub.gotHash, hash)
	}
	if claims.TenantID != 42 || claims.UserID != 9 {
		t.Errorf("claims = tenant %d/user %d, want 42/9", claims.TenantID, claims.UserID)
	}
	// Role comes from the memberships JOIN, not from the token row — that's what
	// makes a demotion take effect immediately.
	if claims.Role != RoleProjectManager {
		t.Errorf("role = %q, want %q", claims.Role, RoleProjectManager)
	}
	// Scope must be tenant, or auth.Middleware's org-scope guard would reject it.
	if claims.Scope != ScopeTenant {
		t.Errorf("scope = %q, want %q", claims.Scope, ScopeTenant)
	}
	if info.TokenID != 7 || info.Capability != CapabilityRead {
		t.Errorf("PATInfo = %+v, want token 7 / read", info)
	}
	// The owner's email rides along so PAT requests carry user_email in error
	// notices, same as session requests.
	if info.UserEmail != "pat-owner@x.test" {
		t.Errorf("PATInfo.UserEmail = %q, want the token owner's email", info.UserEmail)
	}
}

func TestPATResolverRejectsUnknownToken(t *testing.T) {
	// The query filters revoked, expired, non-member and deactivated cases, so
	// they all surface as ErrNoRows. Every one must become the same opaque
	// failure: a probing client must not learn *why* its token was refused.
	stub := &stubTokenQuerier{err: pgx.ErrNoRows}
	resolve := NewPATResolver(stub)

	raw, _, _, _ := GeneratePAT()
	claims, info, err := resolve(context.Background(), raw)
	if !errors.Is(err, ErrInvalidPAT) {
		t.Fatalf("err = %v, want ErrInvalidPAT", err)
	}
	if claims != nil || info != nil {
		t.Error("resolver must return nil claims/info on failure")
	}
	if stub.touched != 0 {
		t.Error("must not touch last_used_at for a token that failed to resolve")
	}
}

func TestPATResolverTouchCoarsening(t *testing.T) {
	// A token used seconds ago must not cost a row write — otherwise every
	// request from a busy agent is an UPDATE on the hot path.
	recent := &stubTokenQuerier{row: db.GetAPITokenAuthRow{
		TokenID: 1, TenantID: 1, UserID: 1, Role: RoleOwner,
		LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}}
	raw, _, _, _ := GeneratePAT()
	if _, _, err := NewPATResolver(recent)(context.Background(), raw); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if recent.touched != 0 {
		t.Errorf("touched %d times for a recently-used token, want 0", recent.touched)
	}

	stale := &stubTokenQuerier{row: db.GetAPITokenAuthRow{
		TokenID: 1, TenantID: 1, UserID: 1, Role: RoleOwner,
		LastUsedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}}
	if _, _, err := NewPATResolver(stale)(context.Background(), raw); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if stale.touched != 1 {
		t.Errorf("touched %d times for a stale token, want 1", stale.touched)
	}

	// A never-used token must be touched.
	fresh := &stubTokenQuerier{row: db.GetAPITokenAuthRow{
		TokenID: 1, TenantID: 1, UserID: 1, Role: RoleOwner,
	}}
	if _, _, err := NewPATResolver(fresh)(context.Background(), raw); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if fresh.touched != 1 {
		t.Errorf("touched %d times for a never-used token, want 1", fresh.touched)
	}
}

func TestPATResolverSurvivesTouchFailure(t *testing.T) {
	// Bookkeeping must never fail an otherwise-valid request.
	stub := &stubTokenQuerier{
		row:      db.GetAPITokenAuthRow{TokenID: 1, TenantID: 5, UserID: 6, Role: RoleAdmin},
		touchErr: errors.New("database on fire"),
	}
	raw, _, _, _ := GeneratePAT()
	claims, _, err := NewPATResolver(stub)(context.Background(), raw)
	if err != nil {
		t.Fatalf("a failed last_used_at write must not fail auth, got %v", err)
	}
	if claims.TenantID != 5 {
		t.Errorf("tenant = %d, want 5", claims.TenantID)
	}
}

func TestPATInfoFromContextAbsent(t *testing.T) {
	// A browser-JWT request has no PATInfo. Unlike the claims accessors this must
	// return nil rather than panicking — "authenticated, but not via PAT" is a
	// legitimate state on every existing route.
	if info := PATInfoFromContext(context.Background()); info != nil {
		t.Errorf("expected nil PATInfo, got %+v", info)
	}
}
