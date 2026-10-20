package notification

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"

	"openloc/internal/emaillayout"
)

// Gmail SMTP with TLS from the first byte on 465.
const (
	gmailHost = "smtp.gmail.com"
	gmailPort = 465
	// gmailScope is the full-access Gmail scope — the one the service account
	// must be domain-wide-delegated for in Workspace Admin.
	gmailScope = "https://mail.google.com/"
)

// GmailSender sends email through Gmail SMTP authenticated with an OAuth2
// service-account token (domain-wide delegation), impersonating a real
// Workspace mailbox configured by the NOTIFICATION_* credentials.
// Satisfies notification.Sender.
type GmailSender struct {
	ts     oauth2.TokenSource
	from   string
	server smtpServer
}

// NewGmailSender builds the SMTP OAuth2 sender.
//
//   - serviceClient: the service-account identifier used as the JWT issuer
//     (env `NOTIFICATION_SERVICE_CLIENT_ID`).
//   - privateKey: the service-account PEM private key (raw, base64, or with
//     literal "\n" escapes).
//   - user: the Workspace mailbox to impersonate + From address
//     (`NOTIFICATION_EMAIL`).
func NewGmailSender(ctx context.Context, serviceClient, privateKey, user string) (*GmailSender, error) {
	if user == "" {
		return nil, fmt.Errorf("gmail: NOTIFICATION_EMAIL (impersonated mailbox) is required")
	}
	if strings.TrimSpace(serviceClient) == "" {
		return nil, fmt.Errorf("gmail: NOTIFICATION_SERVICE_CLIENT_ID is required")
	}
	key, err := normalizePrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	// Service-account JWT (2-legged) with Subject = the impersonated user and
	// the full Gmail scope, exchanged for an XOAUTH2 access token.
	cfg := &jwt.Config{
		Email:      strings.TrimSpace(serviceClient),
		PrivateKey: key,
		Subject:    user,
		Scopes:     []string{gmailScope},
		TokenURL:   google.JWTTokenURL,
	}
	return newGmailSender(ctx, cfg, sendTimeout), nil
}

// newGmailSender sends as cfg.Subject. Token requests get their own HTTP
// timeout: they run before delivery starts its clock, and x/oauth2's default
// client has none.
func newGmailSender(ctx context.Context, cfg *jwt.Config, tokenTimeout time.Duration) *GmailSender {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: tokenTimeout})
	return &GmailSender{
		ts:     cfg.TokenSource(ctx),
		from:   cfg.Subject,
		server: smtpServer{host: gmailHost, port: gmailPort, tls: TLSModeTLS},
	}
}

func (s *GmailSender) SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error {
	tok, err := s.ts.Token()
	if err != nil {
		return fmt.Errorf("gmail: obtain OAuth2 token: %w", err)
	}
	msg, err := buildEmail(s.from, to, subject, body)
	if err != nil {
		return fmt.Errorf("gmail: %w", err)
	}
	return deliver(ctx, s.server, xoauth2Auth{user: s.from, token: tok.AccessToken}, "gmail", s.from, to, msg)
}

// xoauth2Auth implements SASL XOAUTH2 for net/smtp (Gmail's OAuth2 mechanism).
type xoauth2Auth struct {
	user  string
	token string
}

func (a xoauth2Auth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	// "user=<email>^Aauth=Bearer <token>^A^A" where ^A is Ctrl-A (0x01).
	resp := "user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"
	return "XOAUTH2", []byte(resp), nil
}

func (a xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		// A continuation here means the server rejected the token and sent a
		// base64 error challenge; abort with a useful message.
		return nil, fmt.Errorf("gmail: XOAUTH2 rejected: %s", string(fromServer))
	}
	return nil, nil
}

// normalizePrivateKey turns the env-stored private key into PEM bytes. Accepts
// raw PEM, PEM with literal "\n" escapes (common in env vars / JSON), or
// base64-encoded PEM.
func normalizePrivateKey(pk string) ([]byte, error) {
	pk = strings.TrimSpace(pk)
	if pk == "" {
		return nil, fmt.Errorf("gmail: NOTIFICATION_SERVICE_PRIVATE_KEY is empty")
	}
	pk = strings.ReplaceAll(pk, "\\n", "\n")
	if strings.Contains(pk, "BEGIN") {
		return []byte(pk), nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(pk); err == nil && strings.Contains(string(decoded), "BEGIN") {
		return decoded, nil
	}
	return nil, fmt.Errorf("gmail: NOTIFICATION_SERVICE_PRIVATE_KEY is not a PEM block or base64-encoded PEM")
}
