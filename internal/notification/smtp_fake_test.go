package notification

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// fakeSMTP is a real SMTP server (emersion/go-smtp) on 127.0.0.1 that records
// what clients send, so the senders are tested against a real STARTTLS and
// AUTH dialog.
type fakeSMTP struct {
	host string
	port int
	pool *x509.CertPool // trusts the server's self-signed certificate
	opts fakeOptions

	mu     sync.Mutex
	helos  []string // EHLO names, one per session; STARTTLS starts a new one
	logins []fakeLogin
	mails  []fakeMail
}

type fakeOptions struct {
	mechs       []string // AUTH mechanisms offered; empty = no AUTH
	implicitTLS bool     // TLS from the first byte
	noTLS       bool     // plain text only, STARTTLS not offered
	username    string   // accepted PLAIN/LOGIN credentials
	password    string
	token       string // accepted XOAUTH2 token
	refuseRcpt  bool   // answer RCPT TO with 550
}

type fakeLogin struct{ mech, username, secret string }

type fakeMail struct {
	from string
	to   []string
	data []byte
}

func startFakeSMTP(t *testing.T, opts fakeOptions) *fakeSMTP {
	t.Helper()
	cert, pool := selfSignedCert(t)
	l, port := listenLocal(t)
	f := &fakeSMTP{host: localHost, port: port, pool: pool, opts: opts}

	s := gosmtp.NewServer(gosmtp.BackendFunc(func(c *gosmtp.Conn) (gosmtp.Session, error) {
		f.mu.Lock()
		f.helos = append(f.helos, c.Hostname())
		f.mu.Unlock()
		return &fakeSession{srv: f}, nil
	}))
	s.Domain = "fake.test"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	switch {
	case opts.implicitTLS:
		l = tls.NewListener(l, tlsConfig)
	case opts.noTLS:
		// Offer AUTH over plain text, so a client that logs in without TLS
		// would succeed and show up in f.logins.
		s.AllowInsecureAuth = true
	default:
		s.TLSConfig = tlsConfig
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close fake SMTP: %v", err)
		}
		if err := <-done; err != nil && !errors.Is(err, gosmtp.ErrServerClosed) {
			t.Errorf("fake SMTP serve: %v", err)
		}
	})
	return f
}

func (f *fakeSMTP) server(rootCAs *x509.CertPool, mode string) smtpServer {
	return smtpServer{host: f.host, port: f.port, tls: mode, rootCAs: rootCAs}
}

func (f *fakeSMTP) heloNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.helos...)
}

func (f *fakeSMTP) recorded() ([]fakeLogin, []fakeMail) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeLogin(nil), f.logins...), append([]fakeMail(nil), f.mails...)
}

func (f *fakeSMTP) login(mech, username, secret string) error {
	f.mu.Lock()
	f.logins = append(f.logins, fakeLogin{mech, username, secret})
	f.mu.Unlock()
	ok := username == f.opts.username && secret == f.opts.password
	if mech == "XOAUTH2" {
		ok = f.opts.token != "" && secret == f.opts.token
	}
	if !ok {
		return gosmtp.ErrAuthFailed
	}
	return nil
}

type fakeSession struct {
	srv  *fakeSMTP
	from string
	to   []string
}

func (s *fakeSession) AuthMechanisms() []string { return s.srv.opts.mechs }

func (s *fakeSession) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error {
			return s.srv.login(mech, username, password)
		}), nil
	case "LOGIN":
		return &loginServer{check: func(u, p string) error { return s.srv.login(mech, u, p) }}, nil
	case "XOAUTH2":
		return &xoauth2Server{check: func(u, tok string) error { return s.srv.login(mech, u, tok) }}, nil
	}
	return nil, gosmtp.ErrAuthUnknownMechanism
}

func (s *fakeSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *fakeSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if s.srv.opts.refuseRcpt {
		return &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"}
	}
	s.to = append(s.to, to)
	return nil
}

func (s *fakeSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.srv.mu.Lock()
	s.srv.mails = append(s.srv.mails, fakeMail{from: s.from, to: s.to, data: data})
	s.srv.mu.Unlock()
	return nil
}

func (s *fakeSession) Reset()        { s.from, s.to = "", nil }
func (s *fakeSession) Logout() error { return nil }

// loginServer is the server side of SASL LOGIN (go-sasl ships only a client).
type loginServer struct {
	step     int
	username string
	check    func(username, password string) error
}

func (l *loginServer) Next(response []byte) ([]byte, bool, error) {
	l.step++
	switch l.step {
	case 1:
		return []byte("Username:"), false, nil
	case 2:
		l.username = string(response)
		return []byte("Password:"), false, nil
	}
	return nil, true, l.check(l.username, string(response))
}

// xoauth2Server is the server side of Google's XOAUTH2:
// "user=<email>\x01auth=Bearer <token>\x01\x01" as the initial response.
type xoauth2Server struct {
	check func(user, token string) error
}

func (x *xoauth2Server) Next(response []byte) ([]byte, bool, error) {
	var user, token string
	for _, field := range bytes.Split(response, []byte{0x01}) {
		f := string(field)
		switch {
		case strings.HasPrefix(f, "user="):
			user = strings.TrimPrefix(f, "user=")
		case strings.HasPrefix(f, "auth=Bearer "):
			token = strings.TrimPrefix(f, "auth=Bearer ")
		}
	}
	return nil, true, x.check(user, token)
}

// selfSignedCert makes a certificate for localHost and a pool that trusts it.
func selfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP(localHost)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// silentServer accepts connections and never answers, like a firewall that
// swallows packets after the handshake.
func silentServer(t *testing.T) (string, int) {
	t.Helper()
	l, port := listenLocal(t)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return localHost, port
}

// quitFailingServer accepts one message in plain text without a login, then
// answers QUIT with 421, like a server that closes right after DATA. It
// returns the message it accepted on the channel.
func quitFailingServer(t *testing.T) (string, int, <-chan []byte) {
	t.Helper()
	l, port := listenLocal(t)
	t.Cleanup(func() { l.Close() })
	got := make(chan []byte, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		if tp.PrintfLine("220 fake.test ready") != nil {
			return
		}
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			reply := "250 OK"
			switch verb {
			case "DATA":
				if tp.PrintfLine("354 Go ahead") != nil {
					return
				}
				data, err := tp.ReadDotBytes()
				if err != nil {
					return
				}
				got <- data
			case "QUIT":
				reply = "421 Closing"
			}
			if tp.PrintfLine("%s", reply) != nil || verb == "QUIT" {
				return
			}
		}
	}()
	return localHost, port, got
}

// localHost is where every fake server listens; its certificate names it.
const localHost = "127.0.0.1"

// listenLocal opens a listener on a free port of localHost.
func listenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()
	l, err := net.Listen("tcp", localHost+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return l, l.Addr().(*net.TCPAddr).Port
}
