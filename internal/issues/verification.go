package issues

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/signedtoken"
)

// The results of the assistant's check of a report it wrote: whether it could
// confirm the problem from the user's words, the data and the screenshot.
const (
	VerificationVerified      = "verified"
	VerificationNotReproduced = "not_reproduced"
	VerificationUnverified    = "unverified"
)

var verificationLabels = map[string]string{
	VerificationVerified:      "Verified",
	VerificationNotReproduced: "Not reproduced",
	VerificationUnverified:    "Unverified",
}

// maxVerificationNote is the length of the one line of evidence.
const maxVerificationNote = 300

// verificationTTL is how long the report form may hold the assistant's check
// before sending it: the user may take a while to edit the text and add files.
const verificationTTL = time.Hour

// Verification is the assistant's check of a report: a result and one line of
// what it saw. The zero value is no check.
type Verification struct {
	Result string
	Note   string
}

// clean validates a check: a known result with a one-line note of 1 to 300
// characters.
func (v Verification) clean() (Verification, error) {
	if v.Result == "" && strings.TrimSpace(v.Note) == "" {
		return Verification{}, nil
	}
	if verificationLabels[v.Result] == "" {
		return Verification{}, httputil.NewClientError("The verification is verified, not_reproduced or unverified.")
	}
	// One line: a form post turns a line break into CRLF, and the signed note
	// must read the same on both sides.
	note, err := cleanText(strings.Join(strings.Fields(v.Note), " "), "The verification note", 1, maxVerificationNote)
	if err != nil {
		return Verification{}, err
	}
	return Verification{Result: v.Result, Note: note}, nil
}

// WithVerificationKey sets the key that signs the assistant's checks. Every
// replica needs the same key; build it with DeriveVerificationKey. Without
// it a check only verifies on the process that signed it.
func (s *Service) WithVerificationKey(key []byte) *Service {
	s.verifyKey = key
	return s
}

// DeriveVerificationKey turns an application secret into the verification
// key, domain-separated from every other key made from the same secret.
func DeriveVerificationKey(secret []byte) []byte {
	return signedtoken.DeriveKey(secret, "openloc issue verification v1")
}

// verificationPayload is what a check's token is signed for: the member, the
// token's own id, the result and the note.
func verificationPayload(m Member, id string, v Verification) string {
	return fmt.Sprintf("%d|%d|%s|%s|%x", m.TenantID, m.UserID, id, v.Result, sha256.Sum256([]byte(v.Note)))
}

// SignVerification checks v and returns a token that binds it to the member:
// "<id>.<signed token>". The report form sends the token with the report, so
// the browser cannot claim a result the assistant did not give, and the id
// lets the check land on one report only. Empty for no check.
func (s *Service) SignVerification(m Member, v Verification) (string, error) {
	v, err := v.clean()
	if err != nil || v.Result == "" {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("verification token id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	return id + "." + signedtoken.Sign(s.verifyKey, s.now().Add(verificationTTL), verificationPayload(m, id, v)), nil
}

// acceptedCheck is the check a new issue keeps and the id of the token that
// proved it.
type acceptedCheck struct {
	Verification
	tokenID string
}

// acceptedVerification is the check a new issue keeps: v when the token
// proves the assistant gave it to this member and has not expired, else none.
// A wrong token drops the check, never the report. Whether the token was
// already used is the issues table's unique index on its id (Create).
func (s *Service) acceptedVerification(ctx context.Context, m Member, v Verification, token string) acceptedCheck {
	v, err := v.clean()
	if err != nil || v.Result == "" {
		return acceptedCheck{}
	}
	id, signed, ok := strings.Cut(token, ".")
	if !ok || id == "" || !signedtoken.Verify(s.verifyKey, s.now(), signed, verificationPayload(m, id, v)) {
		logging.Warn(ctx, nil, "dropped an issue verification without a valid token", "issue-verification-dropped",
			logging.Label("issue.verification", v.Result))
		return acceptedCheck{}
	}
	return acceptedCheck{Verification: v, tokenID: id}
}

// columns is the check as the three nullable issue columns.
func (c acceptedCheck) columns() (result, note, tokenID *string) {
	if c.Result == "" {
		return nil, nil, nil
	}
	return &c.Result, &c.Note, &c.tokenID
}
