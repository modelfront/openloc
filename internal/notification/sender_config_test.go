package notification

import (
	"context"
	"strings"
	"testing"
)

const testPEM = "-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----" // gitleaks:allow

func TestNewSender_Choice(t *testing.T) {
	gmail := EmailConfig{From: "ops@example.com", GmailClientID: "123", GmailPrivateKey: testPEM}
	smtpCfg := EmailConfig{From: "ops@example.com", SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPUsername: "u", SMTPPassword: "p"}

	// The descriptions are the startup log lines deploy/README.md tells
	// operators to look for.
	const console = "No email sender configured — using console email sender (dev mode)"
	tests := []struct {
		name     string
		cfg      EmailConfig
		want     string // "console", "gmail", "smtp"
		wantDesc string
	}{
		{"nothing set", EmailConfig{}, "console", console},
		{"only From", EmailConfig{From: "ops@example.com"}, "console", console},
		{"Gmail without key", EmailConfig{From: "ops@example.com", GmailClientID: "123"}, "console", console},
		{"Gmail", gmail, "gmail", "Gmail API email sender configured (from: ops@example.com)"},
		{"SMTP", smtpCfg, "smtp", "SMTP email sender configured (smtp.example.com:587, starttls, from: ops@example.com)"},
		{"SMTP relay without login", EmailConfig{From: "ops@example.com", SMTPHost: "relay.example.com", SMTPPort: 25}, "smtp",
			"SMTP email sender configured (relay.example.com:25, starttls, from: ops@example.com)"},
		{"SMTP on 465", EmailConfig{From: "ops@example.com", SMTPHost: "smtp.example.com", SMTPPort: 465}, "smtp",
			"SMTP email sender configured (smtp.example.com:465, tls, from: ops@example.com)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, desc, err := NewSender(context.Background(), tt.cfg)
			if err != nil {
				t.Fatalf("NewSender: %v", err)
			}
			var got string
			switch s.(type) {
			case *ConsoleSender:
				got = "console"
			case *GmailSender:
				got = "gmail"
			case *SMTPSender:
				got = "smtp"
			}
			if got != tt.want {
				t.Errorf("sender = %s (%T), want %s", got, s, tt.want)
			}
			if desc != tt.wantDesc {
				t.Errorf("desc = %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}

func TestNewSender_Errors(t *testing.T) {
	base := func(mod func(*EmailConfig)) EmailConfig {
		c := EmailConfig{From: "ops@example.com", SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPUsername: "u", SMTPPassword: "pw-7f3kq9"}
		mod(&c)
		return c
	}
	tests := []struct {
		name    string
		cfg     EmailConfig
		wantErr string
	}{
		{"SMTP and Gmail both set", base(func(c *EmailConfig) { c.GmailClientID = "123"; c.GmailPrivateKey = testPEM }), "not both"},
		{"SMTP and a leftover Gmail key", base(func(c *EmailConfig) { c.GmailPrivateKey = testPEM }), "not both"},
		{"no From", base(func(c *EmailConfig) { c.From = "" }), "NOTIFICATION_EMAIL"},
		{"From with a display name", base(func(c *EmailConfig) { c.From = "Ops <ops@example.com>" }), "NOTIFICATION_EMAIL"},
		{"only username", base(func(c *EmailConfig) { c.SMTPPassword = "" }), "both SMTP_USERNAME and SMTP_PASSWORD"},
		{"only password", base(func(c *EmailConfig) { c.SMTPUsername = "" }), "both SMTP_USERNAME and SMTP_PASSWORD"},
		{"bad TLS mode", base(func(c *EmailConfig) { c.SMTPTLS = "ssl" }), "SMTP_TLS"},
		{"bad port", base(func(c *EmailConfig) { c.SMTPPort = 0 }), "SMTP_PORT"},
		{"password in clear to a remote host", base(func(c *EmailConfig) { c.SMTPTLS = TLSModeNone }), "unencrypted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, err := NewSender(context.Background(), tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v (sender %T), want one containing %q", err, s, tt.wantErr)
			}
			if strings.Contains(err.Error(), tt.cfg.SMTPPassword) && tt.cfg.SMTPPassword != "" {
				t.Errorf("error leaks the password: %v", err)
			}
		})
	}
}

func TestNewSMTPSender_TLSMode(t *testing.T) {
	tests := []struct {
		port int
		tls  string
		want string
	}{
		{587, "", TLSModeStartTLS},
		{25, "", TLSModeStartTLS},
		{465, "", TLSModeTLS},
		{2525, "TLS", TLSModeTLS},
		{465, "starttls", TLSModeStartTLS},
		{25, " none ", TLSModeNone},
	}
	for _, tt := range tests {
		s, err := NewSMTPSender(SMTPConfig{Host: "smtp.example.com", Port: tt.port, TLS: tt.tls, From: "ops@example.com"})
		if err != nil {
			t.Fatalf("port %d tls %q: %v", tt.port, tt.tls, err)
		}
		if s.server.tls != tt.want {
			t.Errorf("port %d tls %q: mode = %q, want %q", tt.port, tt.tls, s.server.tls, tt.want)
		}
	}
}

func TestNewSMTPSender_NoneWithLoginOnLocalhost(t *testing.T) {
	// A local relay (e.g. Mailpit for testing) may take a login in plain text.
	if _, err := NewSMTPSender(SMTPConfig{Host: "localhost", Port: 1025, TLS: TLSModeNone, Username: "u", Password: "p", From: "ops@example.com"}); err != nil {
		t.Errorf("localhost with SMTP_TLS=none and a login: %v", err)
	}
}
