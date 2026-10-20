package oauthserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"openloc/internal/oauthserver"
)

// Resource indicators (RFC 8707) and issuer identification (RFC 9207).
// A token is only ever for this server's /mcp, so a client asking for any
// other audience is refused at every step that names one.

const issuer = "https://app.example.com"

// authorizeLoc walks consent with extra authorize parameters merged in and
// returns the final redirect. A request rejected before consent comes back
// as that redirect directly.
func (f *fixture) authorizeLoc(extra url.Values) *url.URL {
	f.t.Helper()
	q := url.Values{
		"client_id": {f.clientID}, "redirect_uri": {f.redirect},
		"response_type": {"code"}, "state": {"xyz"}, "scope": {"read"},
		"code_challenge":        {oauthserver.PKCEChallengeForTest(verifier)},
		"code_challenge_method": {"S256"},
	}
	for k, v := range extra {
		q[k] = v
	}
	rec := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, f.session())
	if rec.Code == http.StatusFound {
		return mustParse(f.t, rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusOK {
		f.t.Fatalf("consent GET: %d %s", rec.Code, rec.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		f.t.Fatal("no csrf field in consent page")
	}
	form := cloneValues(q)
	form.Set("csrf", m[1])
	form.Set("decision", "approve")
	form.Set("tenant_id", fmt.Sprint(f.tenantA.ID))
	rec = f.do("POST", "/oauth/authorize", strings.NewReader(form.Encode()), f.session())
	if rec.Code != http.StatusFound {
		f.t.Fatalf("consent POST: %d %s", rec.Code, rec.Body.String())
	}
	return mustParse(f.t, rec.Header().Get("Location"))
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad redirect %q: %v", raw, err)
	}
	return u
}

// tokenCall posts a token request; resource is omitted when empty.
func (f *fixture) tokenCall(form url.Values, resource string) (int, map[string]any) {
	f.t.Helper()
	if resource != "" {
		form.Set("resource", resource)
	}
	rec := f.do("POST", "/oauth/token", strings.NewReader(form.Encode()), nil)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("token response is not JSON: %s", rec.Body.String())
	}
	return rec.Code, body
}

func (f *fixture) codeForm(code string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier}, "client_id": {f.clientID}, "redirect_uri": {f.redirect},
	}
}

func TestIntegrationOAuthResourceAcceptedForms(t *testing.T) {
	f := newFixture(t)
	for _, res := range []string{
		"", // clients predating RFC 8707
		"https://app.example.com/mcp",
		"https://app.example.com/mcp/",
		"HTTPS://App.Example.com/mcp",
	} {
		loc := f.authorizeLoc(url.Values{"resource": {res}})
		code := loc.Query().Get("code")
		if code == "" {
			t.Fatalf("resource %q refused at authorize: %s", res, loc)
		}
		if status, body := f.tokenCall(f.codeForm(code), res); status != http.StatusOK {
			t.Fatalf("resource %q refused at exchange: %d %v", res, status, body)
		}
	}
}

func TestIntegrationOAuthResourceForeignRejectedAtAuthorize(t *testing.T) {
	f := newFixture(t)
	for _, res := range []string{
		"https://evil.example/mcp",
		"https://app.example.com",       // the origin is not the resource
		"https://app.example.com/other", // another path on our host
		"http://app.example.com/mcp",    // scheme downgrade
	} {
		loc := f.authorizeLoc(url.Values{"resource": {res}})
		if loc.Query().Get("error") != "invalid_target" || loc.Query().Get("code") != "" {
			t.Fatalf("foreign resource %q not refused: %s", res, loc)
		}
	}
}

func TestIntegrationOAuthResourceForeignRejectedAtToken(t *testing.T) {
	f := newFixture(t)

	// Code bound to our resource, exchanged for another: refused.
	loc := f.authorizeLoc(url.Values{"resource": {"https://app.example.com/mcp"}})
	status, body := f.tokenCall(f.codeForm(loc.Query().Get("code")), "https://evil.example/mcp")
	if status != http.StatusBadRequest || body["error"] != "invalid_target" {
		t.Fatalf("exchange for a different resource: %d %v", status, body)
	}

	// Code with no resource, exchanged naming a foreign one: refused.
	loc = f.authorizeLoc(nil)
	status, body = f.tokenCall(f.codeForm(loc.Query().Get("code")), "https://evil.example/mcp")
	if status != http.StatusBadRequest || body["error"] != "invalid_target" {
		t.Fatalf("exchange naming a foreign resource: %d %v", status, body)
	}

	// Refresh naming a foreign resource: refused; naming ours: rotates.
	loc = f.authorizeLoc(nil)
	tp, rec := f.exchange(loc.Query().Get("code"), verifier)
	if tp == nil {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body.String())
	}
	refresh := func(rt string) url.Values {
		return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {f.clientID}}
	}
	status, body = f.tokenCall(refresh(tp.Refresh), "https://evil.example/mcp")
	if status != http.StatusBadRequest || body["error"] != "invalid_target" {
		t.Fatalf("refresh for a foreign resource: %d %v", status, body)
	}
	if status, body = f.tokenCall(refresh(tp.Refresh), "https://app.example.com/mcp"); status != http.StatusOK {
		t.Fatalf("refresh for our resource: %d %v", status, body)
	}
}

func TestIntegrationOAuthIssuerOnEveryRedirect(t *testing.T) {
	f := newFixture(t)

	rec := f.do("GET", "/.well-known/oauth-authorization-server", nil, nil)
	var meta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("metadata does not advertise iss: %v", meta)
	}

	// wantErr "" means the success redirect, which carries a code instead.
	cases := []struct {
		name, wantErr string
		extra         url.Values
	}{
		{"success", "", nil},
		{"invalid_scope", "invalid_scope", url.Values{"scope": {"admin"}}},
		{"invalid_target", "invalid_target", url.Values{"resource": {"https://evil.example/mcp"}}},
		{"missing PKCE", "invalid_request", url.Values{"code_challenge": {""}}},
	}
	for _, c := range cases {
		loc := f.authorizeLoc(c.extra)
		q := loc.Query()
		if q.Get("error") != c.wantErr || (c.wantErr == "") != (q.Get("code") != "") {
			t.Fatalf("%s: want error %q, got redirect %s", c.name, c.wantErr, loc)
		}
		if got := q.Get("iss"); got != issuer {
			t.Fatalf("%s redirect: iss=%q, want %q (%s)", c.name, got, issuer, loc)
		}
	}
}

func TestIntegrationOAuthResourceDocumentationResolves(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/.well-known/oauth-protected-resource", nil, nil)
	var prm map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &prm); err != nil {
		t.Fatal(err)
	}
	// The docs page's slug is guides/mcp; /docs/mcp is not a page.
	if prm["resource_documentation"] != issuer+"/docs/guides/mcp" {
		t.Fatalf("resource_documentation: %v", prm["resource_documentation"])
	}
}
