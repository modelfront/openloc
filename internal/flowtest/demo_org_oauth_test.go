package flowtest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var demoConsentCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// A demo visit cannot connect an app: the consent screen is refused and an
// approval writes no authorization code into the demo organization.
func TestIntegrationDemoOrgCannotAuthorizeOAuthApps(t *testing.T) {
	f := newDemoFixture(t)
	token := f.enter(t)

	reg := httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`))
	reg.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	f.router.ServeHTTP(regRec, reg)
	var client struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(regRec.Body.Bytes(), &client); err != nil || client.ClientID == "" {
		t.Fatalf("register client: %d %s", regRec.Code, regRec.Body.String())
	}

	sum := sha256.Sum256([]byte("demo-verifier-0123456789-0123456789-0123456789"))
	params := url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"response_type": {"code"}, "state": {"s"}, "scope": {"write"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	send := func(req *http.Request) *httptest.ResponseRecorder {
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		return rec
	}

	consent := send(httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+params.Encode(), nil))
	if consent.Code != http.StatusForbidden || !strings.Contains(consent.Body.String(), "Playground") {
		t.Errorf("consent screen with a demo token = %d, want 403 naming the Playground", consent.Code)
	}

	// The approval, with the CSRF value the consent page handed out if it
	// rendered, is refused the same way.
	form := url.Values{}
	for k, v := range params {
		form[k] = v
	}
	if m := demoConsentCSRF.FindStringSubmatch(consent.Body.String()); m != nil {
		form.Set("csrf", m[1])
	}
	form.Set("decision", "approve")
	form.Set("tenant_id", fmt.Sprint(f.demo.ID))
	post := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approve := send(post)
	if approve.Code != http.StatusForbidden || !strings.Contains(approve.Body.String(), "Playground") {
		t.Errorf("approval with a demo token = %d %s, want 403 naming the Playground",
			approve.Code, approve.Header().Get("Location"))
	}

	var codes int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM oauth_auth_codes WHERE tenant_id = $1`, f.demo.ID).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 0 {
		t.Errorf("%d authorization codes written into the demo organization, want 0", codes)
	}
}
