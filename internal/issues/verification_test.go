package issues

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestVerificationClean(t *testing.T) {
	for name, c := range map[string]struct {
		in   Verification
		want Verification
		ok   bool
	}{
		"none":            {Verification{}, Verification{}, true},
		"verified":        {Verification{"verified", "  Job 9 has 120 segments, the editor shows 100. "}, Verification{"verified", "Job 9 has 120 segments, the editor shows 100."}, true},
		"not reproduced":  {Verification{"not_reproduced", "The export ran."}, Verification{"not_reproduced", "The export ran."}, true},
		"unknown result":  {Verification{"maybe", "x"}, Verification{}, false},
		"note, no result": {Verification{"", "x"}, Verification{}, false},
		"result, no note": {Verification{"unverified", " "}, Verification{}, false},
		"note too long":   {Verification{"verified", strings.Repeat("é", 301)}, Verification{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := c.in.clean()
			if (err == nil) != c.ok || got != c.want {
				t.Errorf("clean(%+v) = %+v, %v; want %+v, ok %v", c.in, got, err, c.want, c.ok)
			}
		})
	}
}

// A check survives only with a token signed for the same member and the same
// result and note, before it expires.
func TestAcceptedVerification(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s := &Service{verifyKey: DeriveVerificationKey([]byte("secret")), now: func() time.Time { return now }}
	m := Member{TenantID: 1, UserID: 2, Role: "linguist"}
	v := Verification{Result: VerificationVerified, Note: "The editor shows 100 of 120 segments."}
	token, err := s.SignVerification(m, v)
	if err != nil || token == "" {
		t.Fatalf("sign: %q, %v", token, err)
	}
	ctx := context.Background()
	if got := s.acceptedVerification(ctx, m, v, token); got.Verification != v || got.tokenID == "" || !strings.HasPrefix(token, got.tokenID+".") {
		t.Fatalf("a valid token dropped the check: %+v", got)
	}

	other := &Service{verifyKey: DeriveVerificationKey([]byte("another secret")), now: s.now}
	foreign, err := other.SignVerification(m, v)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		m     Member
		v     Verification
		token string
	}{
		"no token":           {m, v, ""},
		"garbage":            {m, v, "not-a-token"},
		"another token id":   {m, v, "AAAAAAAAAAAAAAAAAAAAAA" + token[strings.Index(token, "."):]},
		"no token id":        {m, v, token[strings.Index(token, "."):]},
		"another user":       {Member{TenantID: 1, UserID: 3}, v, token},
		"another org":        {Member{TenantID: 9, UserID: 2}, v, token},
		"another result":     {m, Verification{Result: VerificationNotReproduced, Note: v.Note}, token},
		"another note":       {m, Verification{Result: v.Result, Note: "Something else."}, token},
		"another server key": {m, v, foreign},
	} {
		t.Run(name, func(t *testing.T) {
			if got := s.acceptedVerification(ctx, c.m, c.v, c.token); got != (acceptedCheck{}) {
				t.Errorf("kept %+v", got)
			}
		})
	}

	// A line break the model wrote reaches the server as CRLF from the form.
	twoLines := Verification{Result: VerificationVerified, Note: "Row 3 is empty.\nRow 4 too."}
	tok, err := s.SignVerification(m, twoLines)
	if err != nil {
		t.Fatal(err)
	}
	got := s.acceptedVerification(ctx, m, Verification{Result: VerificationVerified, Note: "Row 3 is empty.\r\nRow 4 too."}, tok)
	if got.Note != "Row 3 is empty. Row 4 too." {
		t.Errorf("a two-line note lost its check: %+v", got)
	}

	now = now.Add(verificationTTL + time.Second)
	if got := s.acceptedVerification(ctx, m, v, token); got != (acceptedCheck{}) {
		t.Errorf("an expired token kept %+v", got)
	}
}
