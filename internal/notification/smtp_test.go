package notification

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"openloc/internal/emaillayout"
	"openloc/internal/logging"
)

const (
	testFrom     = "ops@example.com"
	testTo       = "john@example.com"
	testSubject  = "Neue Aufgabe: Übersetzung prüfen"
	testUser     = "mailer"
	testPassword = "s3cret-pw"
)

var testBody = func() emaillayout.Email {
	email, err := emaillayout.Render("", emaillayout.Content{
		Heading: "Hi John",
		Body:    emaillayout.P("%s", strings.Repeat("a long line ", 200)),
	})
	if err != nil {
		panic(err)
	}
	return email
}()

// newTestSMTPSender builds a sender for the fake server that trusts its
// certificate. cfg fields left empty get the fake's host and port and testFrom.
func newTestSMTPSender(t *testing.T, f *fakeSMTP, cfg SMTPConfig) *SMTPSender {
	t.Helper()
	cfg.Host, cfg.Port = f.host, f.port
	if cfg.From == "" {
		cfg.From = testFrom
	}
	s, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	s.server.rootCAs = f.pool
	return s
}

// assertDelivered checks the server got exactly one message from testFrom to
// testTo whose decoded subject and body are the ones sent.
func assertDelivered(t *testing.T, mails []fakeMail) {
	t.Helper()
	if len(mails) != 1 {
		t.Fatalf("server got %d messages, want 1", len(mails))
	}
	m := mails[0]
	if m.from != testFrom || len(m.to) != 1 || m.to[0] != testTo {
		t.Errorf("envelope = MAIL FROM %q RCPT TO %q, want %q → %q", m.from, m.to, testFrom, testTo)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(m.data))
	if err != nil {
		t.Fatalf("parse message: %v\n%s", err, m.data)
	}
	if got := msg.Header.Get("From"); got != "OpenLoc <"+testFrom+">" {
		t.Errorf("From = %q", got)
	}
	if got := msg.Header.Get("To"); got != testTo {
		t.Errorf("To = %q", got)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != testSubject {
		t.Errorf("Subject = %q (err %v), want %q", subject, err, testSubject)
	}
	if _, err := msg.Header.Date(); err != nil {
		t.Errorf("Date header: %v", err)
	}
	if id := msg.Header.Get("Message-ID"); !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID = %q, want <…@example.com>", id)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// The body's line breaks travel as CRLF, and DATA ends the message on a
	// line break of its own.
	got := strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	if got != testBody.HTML() {
		t.Errorf("body = %q, want %q", got, testBody.HTML())
	}
}

func send(t *testing.T, s Sender) error {
	t.Helper()
	return s.SendEmail(context.Background(), testTo, testSubject, testBody)
}

// assertSentWithLogin sends one email through s and checks that the server saw
// exactly the want login, then the message.
func assertSentWithLogin(t *testing.T, f *fakeSMTP, s Sender, want fakeLogin) {
	t.Helper()
	if err := send(t, s); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	logins, mails := f.recorded()
	if len(logins) != 1 || logins[0] != want {
		t.Errorf("logins = %+v, want one %+v", logins, want)
	}
	assertDelivered(t, mails)
}

func TestSMTPSender_StartTLSWithPlain(t *testing.T) {
	// The fake offers AUTH only after STARTTLS, so a successful login also
	// proves the connection was upgraded.
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"LOGIN", "PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	if s.server.tls != TLSModeStartTLS {
		t.Fatalf("mode = %q, want starttls by default", s.server.tls)
	}
	assertSentWithLogin(t, f, s, fakeLogin{"PLAIN", testUser, testPassword})
}

func TestSMTPSender_LoginMechanism(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"LOGIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	assertSentWithLogin(t, f, s, fakeLogin{"LOGIN", testUser, testPassword})
}

// net/smtp greets with "localhost" unless told otherwise; some servers
// penalise that, so the sender gives the machine's name.
func TestSMTPSender_EHLOUsesHostName(t *testing.T) {
	want, err := os.Hostname()
	if err != nil || want == "" {
		t.Skipf("this machine reports no host name (%v), so there is nothing to compare", err)
	}
	f := startFakeSMTP(t, fakeOptions{noTLS: true})
	s := newTestSMTPSender(t, f, SMTPConfig{TLS: TLSModeNone})
	if err := send(t, s); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if helos := f.heloNames(); len(helos) != 1 || helos[0] != want {
		t.Errorf("EHLO names = %q, want [%q]", helos, want)
	}
}

// A threaded email reaches the server with the thread's headers.
func TestSMTPSender_SendsThreadHeaders(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{noTLS: true})
	s := newTestSMTPSender(t, f, SMTPConfig{TLS: TLSModeNone})
	body := testBody.InThread(emaillayout.Thread{ID: "issue-7.app.example.com"})
	if err := s.SendEmail(context.Background(), testTo, testSubject, body); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	_, mails := f.recorded()
	if len(mails) != 1 {
		t.Fatalf("server got %d messages, want 1", len(mails))
	}
	msg, err := mail.ReadMessage(bytes.NewReader(mails[0].data))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	const want = "<issue-7.app.example.com@example.com>"
	if got := msg.Header.Get("In-Reply-To"); got != want {
		t.Errorf("In-Reply-To = %q, want %q", got, want)
	}
	if got := msg.Header.Get("References"); got != want {
		t.Errorf("References = %q, want %q", got, want)
	}
}

func TestSMTPSender_ImplicitTLS(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{implicitTLS: true, mechs: []string{"PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword, TLS: TLSModeTLS})
	if err := send(t, s); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	_, mails := f.recorded()
	assertDelivered(t, mails)
}

func TestSMTPSender_RefusesServerWithoutStartTLS(t *testing.T) {
	// The fake accepts a plain-text login, so any password sent would show up.
	f := startFakeSMTP(t, fakeOptions{noTLS: true, mechs: []string{"PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS, and the password is never sent unencrypted") {
		t.Fatalf("err = %v, want a missing-STARTTLS error that keeps the password encrypted", err)
	}
	if strings.Contains(err.Error(), "SMTP_TLS=none") {
		t.Errorf("err suggests SMTP_TLS=none, which is refused with a login: %v", err)
	}
	logins, mails := f.recorded()
	if len(logins) != 0 || len(mails) != 0 {
		t.Errorf("server got logins %+v and %d messages, want none", logins, len(mails))
	}
}

func TestSMTPSender_MissingStartTLSWithoutLoginSuggestsNone(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{noTLS: true})
	s := newTestSMTPSender(t, f, SMTPConfig{})
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "set SMTP_TLS=none") {
		t.Fatalf("err = %v, want a hint to set SMTP_TLS=none", err)
	}
	if _, mails := f.recorded(); len(mails) != 0 {
		t.Errorf("server got %d messages, want none", len(mails))
	}
}

// A relay on this machine may take a login in plain text, with either
// mechanism; nowhere else may.
func TestSMTPSender_PlainTextLoginToLocalhost(t *testing.T) {
	for _, mech := range []string{"PLAIN", "LOGIN"} {
		t.Run(mech, func(t *testing.T) {
			f := startFakeSMTP(t, fakeOptions{noTLS: true, mechs: []string{mech}, username: testUser, password: testPassword})
			s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword, TLS: TLSModeNone})
			assertSentWithLogin(t, f, s, fakeLogin{mech, testUser, testPassword})
		})
	}
}

func TestSMTPSender_RefusedRecipientLeavesAddressOut(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"PLAIN"}, username: testUser, password: testPassword, refuseRcpt: true})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "RCPT TO") || !strings.Contains(err.Error(), "550") {
		t.Fatalf("err = %v, want the server's 550 answer to RCPT TO", err)
	}
	if strings.Contains(err.Error(), testTo) {
		t.Errorf("error quotes the recipient: %v", err)
	}
}

func TestSMTPSender_QuitFailureAfterAcceptIsSuccess(t *testing.T) {
	var logs bytes.Buffer
	prev := logging.L()
	logging.SetLogger(logging.New(logging.Options{ECS: true, Writer: &logs, Level: slog.LevelDebug, Version: "testrev"}))
	t.Cleanup(func() { logging.SetLogger(prev) })

	host, port, got := quitFailingServer(t)
	s, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: testFrom, TLS: TLSModeNone})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if err := send(t, s); err != nil {
		t.Fatalf("SendEmail = %v after the server accepted the message, want nil", err)
	}
	select {
	case data := <-got:
		assertDelivered(t, []fakeMail{{from: testFrom, to: []string{testTo}, data: data}})
	default:
		t.Fatal("server accepted no message")
	}
	// The refused QUIT is not lost: it is logged once, with the server's answer.
	var quitLogs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m[logging.KeyEventAction] == "email-quit-failed" {
			quitLogs = append(quitLogs, m)
		}
	}
	if len(quitLogs) != 1 {
		t.Fatalf("got %d email-quit-failed log lines, want 1:\n%s", len(quitLogs), logs.String())
	}
	if msg, _ := quitLogs[0]["error.message"].(string); !strings.Contains(msg, "421") {
		t.Errorf("email-quit-failed error = %q, want the server's 421", msg)
	}
}

func TestSMTPSender_WrongPassword(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: "wrong-pw-123"})
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("err = %v, want the server's 535 answer", err)
	}
	if strings.Contains(err.Error(), "wrong-pw-123") {
		t.Errorf("error leaks the password: %v", err)
	}
	if _, mails := f.recorded(); len(mails) != 0 {
		t.Errorf("server got %d messages, want none", len(mails))
	}
}

func TestSMTPSender_UnsupportedMechanism(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"CRAM-MD5"}})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "CRAM-MD5") {
		t.Fatalf("err = %v, want an error naming what the server offers", err)
	}
}

func TestSMTPSender_RelayWithoutLogin(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{noTLS: true})
	s := newTestSMTPSender(t, f, SMTPConfig{TLS: TLSModeNone})
	if err := send(t, s); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	logins, mails := f.recorded()
	if len(logins) != 0 {
		t.Errorf("logins = %+v, want none", logins)
	}
	assertDelivered(t, mails)
}

func TestSMTPSender_RejectsUntrustedCertificate(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	s.server.rootCAs = nil // system roots don't know the fake's certificate
	err := send(t, s)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v, want a STARTTLS certificate error", err)
	}
	logins, mails := f.recorded()
	if len(logins) != 0 || len(mails) != 0 {
		t.Errorf("server got logins %+v and %d messages, want none", logins, len(mails))
	}
}

func TestSMTPSender_StopsAtContextDeadline(t *testing.T) {
	host, port := silentServer(t)
	s, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: testFrom})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.SendEmail(ctx, testTo, testSubject, testBody)
	if err == nil {
		t.Fatal("SendEmail to a silent server succeeded")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("SendEmail took %v, want it to stop near the 300ms deadline", elapsed)
	}
}

func TestSMTPSender_StopsAtSendLimit(t *testing.T) {
	host, port := silentServer(t)
	s, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: testFrom})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	s.server.timeout = 200 * time.Millisecond
	start := time.Now()
	if err := send(t, s); err == nil {
		t.Fatal("SendEmail to a silent server succeeded")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("SendEmail took %v without a context deadline, want it to stop near the 200ms limit", elapsed)
	}
}

func TestSMTPSender_SendsAfterRequestCancelled(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{mechs: []string{"PLAIN"}, username: testUser, password: testPassword})
	s := newTestSMTPSender(t, f, SMTPConfig{Username: testUser, Password: testPassword})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SendEmail(ctx, testTo, testSubject, testBody); err != nil {
		t.Fatalf("SendEmail with a cancelled context: %v", err)
	}
	_, mails := f.recorded()
	assertDelivered(t, mails)
}

// Every login in the delivery tests goes to 127.0.0.1, where plain text is
// allowed too; this pins the rule for a remote server.
func TestPasswordAuth_RemoteServerNeedsTLS(t *testing.T) {
	for _, mech := range []string{"PLAIN", "LOGIN"} {
		for _, encrypted := range []bool{true, false} {
			a := &passwordAuth{host: "mail.example.com", username: testUser, password: testPassword}
			got, _, err := a.Start(&smtp.ServerInfo{Name: "mail.example.com", TLS: encrypted, Auth: []string{mech}})
			switch {
			case encrypted && (err != nil || got != mech):
				t.Errorf("%s over TLS: Start = %q, %v; want %q, nil", mech, got, err, mech)
			case !encrypted && err == nil:
				t.Errorf("%s over plain text to a remote host: Start succeeded", mech)
			}
		}
	}
}

func TestGmailSender_DeliversWithXOAUTH2(t *testing.T) {
	f := startFakeSMTP(t, fakeOptions{implicitTLS: true, mechs: []string{"XOAUTH2"}, token: "tok123"})
	g := &GmailSender{
		ts:     oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok123"}),
		from:   testFrom,
		server: f.server(f.pool, TLSModeTLS),
	}
	assertSentWithLogin(t, f, g, fakeLogin{"XOAUTH2", testFrom, "tok123"})
}

func TestNewGmailSender_ProductionServer(t *testing.T) {
	g, err := NewGmailSender(context.Background(), "sa@proj.iam.gserviceaccount.com", testPEM, testFrom)
	if err != nil {
		t.Fatalf("NewGmailSender: %v", err)
	}
	if want := (smtpServer{host: "smtp.gmail.com", port: 465, tls: "tls"}); g.server != want {
		t.Errorf("server = %+v, want %+v", g.server, want)
	}
}

func TestGmailSender_TokenFetchStopsAtLimit(t *testing.T) {
	release := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // never answers
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(tokenServer.Close)
	t.Cleanup(func() { close(release) })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	cfg := &jwt.Config{
		Email:      "sa@proj.iam.gserviceaccount.com",
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		Subject:    testFrom,
		Scopes:     []string{gmailScope},
		TokenURL:   tokenServer.URL,
	}
	g := newGmailSender(context.Background(), cfg, 200*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- send(t, g) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "obtain OAuth2 token") {
			t.Errorf("err = %v, want a token error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendEmail still waiting on the token server after 5s, want it to stop near the 200ms limit")
	}
}
