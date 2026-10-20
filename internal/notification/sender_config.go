package notification

import (
	"context"
	"errors"
	"fmt"
)

// EmailConfig is the deployment's email settings, read from the environment.
type EmailConfig struct {
	From string // NOTIFICATION_EMAIL — the From address for every sender

	SMTPHost     string // SMTP_HOST — turns the SMTP sender on
	SMTPPort     int    // SMTP_PORT
	SMTPUsername string // SMTP_USERNAME
	SMTPPassword string // SMTP_PASSWORD
	SMTPTLS      string // SMTP_TLS

	GmailClientID   string // NOTIFICATION_SERVICE_CLIENT_ID
	GmailPrivateKey string // NOTIFICATION_SERVICE_PRIVATE_KEY
}

// NewSender picks the deployment's one email sender: SMTP when SMTP_HOST is
// set, Gmail when the Google service account is, otherwise the console. Both
// at once is an error, so a deployment never sends through a server nobody
// meant to use. The string describes the choice for the startup log.
func NewSender(ctx context.Context, cfg EmailConfig) (Sender, string, error) {
	gmailSet := cfg.GmailClientID != "" || cfg.GmailPrivateKey != ""
	switch {
	case cfg.SMTPHost != "" && gmailSet:
		return nil, "", errors.New("email: set SMTP_HOST or the Google service account (NOTIFICATION_SERVICE_CLIENT_ID, NOTIFICATION_SERVICE_PRIVATE_KEY), not both")
	case cfg.SMTPHost != "":
		s, err := NewSMTPSender(SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.From,
			TLS:      cfg.SMTPTLS,
		})
		if err != nil {
			return nil, "", err
		}
		return s, fmt.Sprintf("SMTP email sender configured (%s, %s, from: %s)", s.server.addr(), s.server.tls, s.from), nil
	case cfg.From != "" && cfg.GmailClientID != "" && cfg.GmailPrivateKey != "":
		s, err := NewGmailSender(ctx, cfg.GmailClientID, cfg.GmailPrivateKey, cfg.From)
		if err != nil {
			return nil, "", err
		}
		return s, fmt.Sprintf("Gmail API email sender configured (from: %s)", cfg.From), nil
	}
	return &ConsoleSender{}, "No email sender configured — using console email sender (dev mode)", nil
}
