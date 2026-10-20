package notification

import (
	"context"

	"openloc/internal/emaillayout"
	"openloc/internal/logging"
)

// Sender delivers an email. Implementations: SMTPSender (any SMTP server, see
// smtp.go), GmailSender (Gmail SMTP with a service-account OAuth2 token, see
// gmail.go) and ConsoleSender (dev fallback — logs the email instead of
// sending it). NewSender picks one from the deployment's settings.
type Sender interface {
	SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error
}

// ConsoleSender logs emails to stdout instead of sending them. Used when no
// email provider is configured (dev / local).
type ConsoleSender struct{}

func (s *ConsoleSender) SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error {
	logging.Info(ctx, "dev email not sent: no provider configured", "notification-dev-email", logging.Label("to", to), logging.Label("subject", subject))
	return nil
}
