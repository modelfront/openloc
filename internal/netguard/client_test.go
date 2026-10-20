package netguard

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Outside development the client refuses a loopback server outright.
func TestClientRefusesLoopbackOutsideDev(t *testing.T) {
	t.Setenv("ENV", "production")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Client(5 * time.Second).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("client reached a loopback server outside development")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("err = %v, want the non-public refusal", err)
	}
}

// Development reaches loopback: the dev stack's local engines and gateways
// live there.
func TestClientAllowsLoopbackInDev(t *testing.T) {
	for _, env := range []string{"development", ""} {
		t.Setenv("ENV", env)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		resp, err := Client(5 * time.Second).Get(srv.URL)
		srv.Close()
		if err != nil {
			t.Fatalf("ENV=%q: %v", env, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Fatalf("ENV=%q: status %d", env, resp.StatusCode)
		}
	}
}

// A redirect is dialled through the same check, so an allowed host cannot
// bounce the request onto a refused one. The allowed host here stands in for a
// public vendor endpoint that 302s to an internal service.
func TestClientChecksEveryRedirectHop(t *testing.T) {
	inner, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	reached := make(chan struct{}, 1)
	internal := &httptest.Server{Listener: inner, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached <- struct{}{}
		_, _ = w.Write([]byte("secret file"))
	})}}
	internal.Start()
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/buckets/tenant-1/file", http.StatusFound)
	}))
	defer redirector.Close()

	only127 := func(ip net.IP) bool { return ip.Equal(net.IPv4(127, 0, 0, 1)) }
	c := &http.Client{Timeout: 5 * time.Second, Transport: transport(only127)}
	resp, err := c.Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("redirect to a refused address was followed")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("err = %v, want the non-public refusal", err)
	}
	select {
	case <-reached:
		t.Fatal("the internal server was reached")
	default:
	}
}
