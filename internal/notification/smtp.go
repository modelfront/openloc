package notification

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"openloc/internal/emaillayout"
	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// TLS modes for SMTPConfig.TLS (env SMTP_TLS).
const (
	// TLSModeStartTLS connects in plain text and upgrades with STARTTLS. The
	// server must offer it; nothing is sent before the upgrade.
	TLSModeStartTLS = "starttls"
	// TLSModeTLS speaks TLS from the first byte, usually on port 465.
	TLSModeTLS = "tls"
	// TLSModeNone never encrypts. Credentials are refused unless the server is
	// on this machine, so it suits only a relay that needs no login.
	TLSModeNone = "none"
)

// sendTimeout caps one delivery: dial, TLS, login and DATA together. Emails go
// out inside the HTTP request that triggers them, so a server that stops
// answering must not hold the request open.
const sendTimeout = 30 * time.Second

// SMTPConfig is a generic SMTP server the deployment sends email through.
type SMTPConfig struct {
	Host     string // SMTP_HOST
	Port     int    // SMTP_PORT
	Username string // SMTP_USERNAME; empty with Password = no login
	Password string // SMTP_PASSWORD
	From     string // NOTIFICATION_EMAIL
	TLS      string // SMTP_TLS; empty = TLSModeTLS on port 465, else TLSModeStartTLS
}

// SMTPSender sends email through any SMTP server, logging in with a username
// and password (PLAIN or LOGIN) or not at all. Satisfies notification.Sender.
type SMTPSender struct {
	server   smtpServer
	username string
	password string
	from     string
}

// smtpServer is where a sender connects and how it secures the connection.
type smtpServer struct {
	host    string
	port    int
	tls     string
	rootCAs *x509.CertPool // nil = the system roots
	timeout time.Duration  // 0 = sendTimeout
}

func (s smtpServer) addr() string {
	return net.JoinHostPort(s.host, strconv.Itoa(s.port))
}

func (s smtpServer) limit() time.Duration {
	if s.timeout > 0 {
		return s.timeout
	}
	return sendTimeout
}

// NewSMTPSender validates the settings. It does not connect: the server checks
// the credentials on the first email.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, errors.New("smtp: SMTP_HOST is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp: SMTP_PORT %d is not a valid port", cfg.Port)
	}
	from := strings.TrimSpace(cfg.From)
	if from == "" {
		return nil, errors.New("smtp: NOTIFICATION_EMAIL (the From address) is required")
	}
	if addr, err := mail.ParseAddress(from); err != nil || addr.Address != from {
		return nil, fmt.Errorf("smtp: NOTIFICATION_EMAIL %q is not a plain email address", from)
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("smtp: set both SMTP_USERNAME and SMTP_PASSWORD, or neither")
	}
	mode, err := resolveTLSMode(cfg.TLS, cfg.Port)
	if err != nil {
		return nil, err
	}
	if mode == TLSModeNone && cfg.Username != "" && !netguard.IsLocalhost(host) {
		return nil, errors.New("smtp: SMTP_TLS=none would send the password unencrypted; use starttls or tls, or a relay that needs no login")
	}
	return &SMTPSender{
		server:   smtpServer{host: host, port: cfg.Port, tls: mode},
		username: cfg.Username,
		password: cfg.Password,
		from:     from,
	}, nil
}

func resolveTLSMode(mode string, port int) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(mode)); m {
	case "":
		if port == 465 {
			return TLSModeTLS, nil
		}
		return TLSModeStartTLS, nil
	case TLSModeStartTLS, TLSModeTLS, TLSModeNone:
		return m, nil
	}
	return "", fmt.Errorf("smtp: SMTP_TLS %q is not one of %s, %s, %s", mode, TLSModeStartTLS, TLSModeTLS, TLSModeNone)
}

func (s *SMTPSender) SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error {
	msg, err := buildEmail(s.from, to, subject, body)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	var auth smtp.Auth
	if s.username != "" {
		auth = &passwordAuth{host: s.server.host, username: s.username, password: s.password}
	}
	return deliver(ctx, s.server, auth, "smtp", s.from, to, msg)
}

// deliver sends one message: connect, secure the connection as srv.tls says,
// log in when auth is set, then MAIL / RCPT / DATA. prefix names the sender in
// errors.
//
// Only the context's deadline applies, not its cancellation: an email whose
// request the browser abandoned still goes out.
func deliver(ctx context.Context, srv smtpServer, auth smtp.Auth, prefix, from, to string, msg []byte) error {
	deadline := time.Now().Add(srv.limit())
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.Dial("tcp", srv.addr())
	if err != nil {
		return fmt.Errorf("%s: dial %s: %w", prefix, srv.addr(), err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return fmt.Errorf("%s: set deadline: %w", prefix, err)
	}
	tlsConfig := &tls.Config{ServerName: srv.host, RootCAs: srv.rootCAs, MinVersion: tls.VersionTLS12}
	if srv.tls == TLSModeTLS {
		conn = tls.Client(conn, tlsConfig)
	}
	c, err := smtp.NewClient(conn, srv.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("%s: connect to %s: %w", prefix, srv.addr(), err)
	}
	defer c.Close()

	if err := c.Hello(heloName()); err != nil {
		return fmt.Errorf("%s: EHLO: %w", prefix, err)
	}
	if srv.tls == TLSModeStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			if auth != nil {
				return fmt.Errorf("%s: %s does not offer STARTTLS, and the password is never sent unencrypted; use the port your provider gives for STARTTLS (usually 587) or for TLS (usually 465)", prefix, srv.addr())
			}
			return fmt.Errorf("%s: %s does not offer STARTTLS; for a relay that needs no encryption, set SMTP_TLS=none", prefix, srv.addr())
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("%s: STARTTLS: %w", prefix, err)
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("%s: login: %w", prefix, err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("%s: MAIL FROM: %w", prefix, err)
	}
	// The recipient stays out of the error: it reaches error tracking, and
	// every caller logs the address itself.
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("%s: RCPT TO: %w", prefix, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("%s: DATA: %w", prefix, err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("%s: write message: %w", prefix, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("%s: close message: %w", prefix, err)
	}
	// The server has accepted the message. A failed QUIT does not undo that,
	// and reporting it as a failure would make callers drop a link the
	// recipient already has.
	if err := c.Quit(); err != nil {
		logging.Warn(ctx, err, "email accepted, but QUIT failed", "email-quit-failed",
			logging.Label("email_sender", prefix))
	}
	return nil
}

// heloName is the name the client gives in EHLO. Some servers penalise the
// net/smtp default "localhost".
func heloName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "localhost"
}

// passwordAuth logs in with a username and password: PLAIN when the server
// offers it, LOGIN otherwise. Both refuse to run over an unencrypted
// connection unless the server is on this machine.
type passwordAuth struct {
	host     string
	username string
	password string
	plain    smtp.Auth // set by Start when PLAIN is in use
	step     int       // LOGIN challenges answered so far
}

func (a *passwordAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	offers := func(mech string) bool {
		for _, m := range server.Auth {
			if strings.EqualFold(m, mech) {
				return true
			}
		}
		return false
	}
	switch {
	case offers("PLAIN"):
		a.plain = smtp.PlainAuth("", a.username, a.password, a.host)
		return a.plain.Start(server)
	case offers("LOGIN"):
		if !server.TLS && !netguard.IsLocalhost(server.Name) {
			return "", nil, errors.New("unencrypted connection")
		}
		return "LOGIN", nil, nil
	case len(server.Auth) == 0:
		return "", nil, errors.New("the server offers no login; leave SMTP_USERNAME and SMTP_PASSWORD empty for a relay that needs none")
	}
	return "", nil, fmt.Errorf("the server offers %s; only PLAIN and LOGIN are supported", strings.Join(server.Auth, ", "))
}

func (a *passwordAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if a.plain != nil {
		return a.plain.Next(fromServer, more)
	}
	if !more {
		return nil, nil
	}
	// LOGIN asks for the username, then the password. The prompt text varies
	// between servers, so the step decides the answer.
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
}
