// Package suggestion signs the provenance of a text the CAT editor's
// Resources panel offers — a TM match or an MT suggestion — so that inserting
// it can record a TM or MT event without trusting the client.
//
// The panel endpoints sign one token per suggestion; the draft save that
// inserts it hands the token back. A token names the job and the source text
// it was issued for — and the segment, when the lookup named one — so it only
// verifies there, in the tenant it was issued to.
package suggestion

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind is where a suggestion came from.
type Kind string

const (
	KindTM Kind = "tm"
	KindMT Kind = "mt"
)

// TTL is how long a token stays valid. Long enough for a segment left open
// over lunch; the panel refetches long before most tokens expire.
const TTL = 24 * time.Hour

// ErrInvalid is any token that does not verify: malformed, tampered, expired,
// or issued to another tenant.
var ErrInvalid = errors.New("invalid suggestion token")

// Claims is what a token states about one suggestion.
type Claims struct {
	Kind  Kind  `json:"k"`
	JobID int64 `json:"j"`
	// Source is SourceHash of the segment source the suggestion was made for.
	Source string `json:"s"`
	// TranslationID, when set, is the one segment the suggestion was made for:
	// a lookup that knew the segment can depend on it (an in-context 101%, a
	// stored MT), so the token does not verify on a repetition elsewhere.
	TranslationID int64 `json:"tr,omitempty"`

	// TM: the entry and the match percent the panel showed.
	TMID      int64 `json:"tm,omitempty"`
	TMEntryID int64 `json:"te,omitempty"`
	Percent   int16 `json:"p,omitempty"`

	// MT: the engine behind it. Exactly one of the ids is set when known.
	MTEngineID *int64 `json:"me,omitempty"`
	AIEngineID *int64 `json:"ae,omitempty"`
	EngineName string `json:"en,omitempty"`

	Exp int64 `json:"x"`
}

// SourceHash identifies a source text inside a token, byte for byte: a
// segment whose source differs even by surrounding whitespace scores
// differently, so it must not take another source's suggestion.
func SourceHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// strictB64 refuses non-zero padding bits, which the lenient decoder ignores
// and which would otherwise give one signature several spellings.
var strictB64 = base64.RawURLEncoding.Strict()

// Signer issues and verifies tokens with one server-wide key.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner derives the token key from an application secret, domain-separated
// so a suggestion token can never be mistaken for any other signed value.
func NewSigner(secret []byte) *Signer {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("openloc panel suggestion v1"))
	return &Signer{key: mac.Sum(nil), now: time.Now}
}

// Sign returns the token for c in tenantID. Exp is set here.
func (s *Signer) Sign(tenantID int64, c Claims) (string, error) {
	c.Exp = s.now().Add(TTL).Unix()
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal suggestion claims: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return enc + "." + base64.RawURLEncoding.EncodeToString(s.mac(tenantID, enc)), nil
}

// Verify returns the claims of a token issued to tenantID, or ErrInvalid.
func (s *Signer) Verify(tenantID int64, token string) (Claims, error) {
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, ErrInvalid
	}
	got, err := strictB64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(tenantID, enc)) {
		return Claims{}, ErrInvalid
	}
	payload, err := strictB64.DecodeString(enc)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, ErrInvalid
	}
	if s.now().Unix() > c.Exp || (c.Kind != KindTM && c.Kind != KindMT) {
		return Claims{}, ErrInvalid
	}
	return c, nil
}

func (s *Signer) mac(tenantID int64, payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "%d|%s", tenantID, payload)
	return m.Sum(nil)
}
