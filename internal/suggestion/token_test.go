package suggestion

import (
	"strings"
	"testing"
	"time"
)

func testSigner(now time.Time) *Signer {
	s := NewSigner([]byte("test-secret"))
	s.now = func() time.Time { return now }
	return s
}

func TestSignVerify_RoundTrip(t *testing.T) {
	s := testSigner(time.Unix(1_000_000, 0))
	engine := int64(7)
	in := Claims{Kind: KindMT, JobID: 3, Source: SourceHash("Hello"), MTEngineID: &engine, EngineName: "mock"}
	tok, err := s.Sign(11, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(11, tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Kind != KindMT || got.JobID != 3 || got.Source != in.Source ||
		got.MTEngineID == nil || *got.MTEngineID != 7 || got.EngineName != "mock" {
		t.Errorf("claims = %+v", got)
	}
}

func TestVerify_Refuses(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := testSigner(now)
	tok, err := s.Sign(11, Claims{Kind: KindTM, JobID: 3, Source: SourceHash("Hello"), TMID: 1, TMEntryID: 2, Percent: 90})
	if err != nil {
		t.Fatal(err)
	}
	payload, sig, _ := strings.Cut(tok, ".")
	other, err := s.Sign(11, Claims{Kind: KindTM, JobID: 4, Source: SourceHash("Hello"), Percent: 100})
	if err != nil {
		t.Fatal(err)
	}
	otherPayload, _, _ := strings.Cut(other, ".")

	expired := testSigner(now.Add(TTL + time.Second))
	cases := map[string]struct {
		s      *Signer
		tenant int64
		token  string
	}{
		"another tenant":         {s, 12, tok},
		"another key":            {NewSigner([]byte("other-secret")), 11, tok},
		"a swapped payload":      {s, 11, otherPayload + "." + sig},
		"no signature":           {s, 11, payload},
		"a padding-bit spelling": {s, 11, payload + "." + sig[:len(sig)-1] + paddingTwin(sig[len(sig)-1])},
		"an expired token":       {expired, 11, tok},
		"not base64":             {s, 11, "!!.!!"},
		"an empty token":         {s, 11, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.s.Verify(c.tenant, c.token); err != ErrInvalid {
				t.Errorf("Verify = %v, want ErrInvalid", err)
			}
		})
	}
}

// paddingTwin returns another base64url character with the same top two bits:
// the last character of a 32-byte signature carries only those, so the
// lenient decoder reads both characters as the same bytes.
func paddingTwin(c byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, c)
	return string(alphabet[i^1])
}

func TestSourceHash_IsExact(t *testing.T) {
	if SourceHash(" Hello \n") == SourceHash("Hello") {
		t.Error("surrounding whitespace does not change the hash")
	}
	if SourceHash("Hello") != SourceHash("Hello") {
		t.Error("the same source hashes differently")
	}
	if SourceHash("Hello") == SourceHash("Hello!") {
		t.Error("different sources share a hash")
	}
}
