package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"openloc/pkg/extsdk"
)

// leakToken's URL-escaped form differs from the plain one; plainToken is a
// key made only of letters and digits, which is valid inside a host name.
const (
	leakToken  = "s3cr3t/+=tok"
	plainToken = "sk9secretkey42"
)

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func noToken(t *testing.T, where, s string) {
	t.Helper()
	for _, tok := range []string{leakToken, url.QueryEscape(leakToken), plainToken} {
		if strings.Contains(s, tok) {
			t.Fatalf("%s leaks the token: %q", where, s)
		}
	}
}

// A module that puts the placeholder where the host belongs must not get the
// key back in the refusal message.
func TestHTTP_PlaceholderInHostDoesNotLeakTheToken(t *testing.T) {
	for _, token := range []string{leakToken, plainToken} {
		env := &callEnv{extID: "acme", token: token, hosts: map[string]bool{"api.example.com": true}, client: http.DefaultClient}
		placeholderInHost(t, env)
	}
}

func placeholderInHost(t *testing.T, env *callEnv) {
	t.Helper()
	for _, raw := range []string{
		"https://" + extsdk.SecretPlaceholder + "/",
		"https://" + extsdk.SecretPlaceholder + ".example.com/",
		"https://user:" + extsdk.SecretPlaceholder + "@api.example.com/",
	} {
		resp := env.do(context.Background(), extsdk.HTTPRequest{Method: "GET", URL: raw})
		if resp.Error == "" {
			t.Fatalf("%s: want a refusal", raw)
		}
		noToken(t, "refusal for "+raw, resp.Error)
	}
}

// A vendor that echoes the key must not hand it to the module.
func TestHTTP_ResponseEchoingTheTokenIsRedacted(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo", r.URL.Query().Get("token"))
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid key ` + r.URL.Query().Get("token") + `"}`))
	}))
	defer srv.Close()
	env := &callEnv{extID: "acme", token: leakToken, hosts: map[string]bool{hostOf(t, srv.URL): true}, client: srv.Client()}
	resp := env.do(context.Background(), extsdk.HTTPRequest{Method: "GET", URL: srv.URL + "/check?token=" + extsdk.SecretPlaceholder})
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d (%s)", resp.Status, resp.Error)
	}
	noToken(t, "response body", string(resp.Body))
	for k, v := range resp.Headers {
		noToken(t, "response header "+k, v)
	}
}
