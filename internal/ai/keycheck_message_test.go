package ai

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// A failed key check reads as one plain sentence: the vendor's display name and
// the next step, never the provider's raw body.
func TestKeyCheckReason(t *testing.T) {
	const body = `{"error":{"message":"Internal server error","code":null,"status":500}}`
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unauthorized", &callFailure{status: http.StatusUnauthorized, msg: "invalid API key (status 401)"},
			"OpenAI rejected this API key. Check that you copied the whole key."},
		{"forbidden", &callFailure{status: http.StatusForbidden, msg: "invalid API key (status 403)"},
			"OpenAI rejected this API key. Check that you copied the whole key."},
		{"transport", &callFailure{transport: true, msg: "could not reach provider: dial tcp"},
			"Could not reach OpenAI. Check the endpoint address and try again."},
		{"rate limited", &callFailure{status: http.StatusTooManyRequests, msg: "provider returned status 429: " + body},
			"OpenAI is limiting requests for this key. Wait a minute and try again."},
		{"not found", &callFailure{status: http.StatusNotFound, msg: "provider returned status 404: " + body},
			"OpenAI could not find this endpoint. Check the endpoint address."},
		{"server error", &callFailure{status: http.StatusInternalServerError, msg: "provider returned status 500: " + body},
			"OpenAI is having a problem right now. Try again in a few minutes."},
		{"bad gateway", &callFailure{status: http.StatusBadGateway, msg: "provider returned status 502: " + body},
			"OpenAI is having a problem right now. Try again in a few minutes."},
		{"other status", &callFailure{status: http.StatusBadRequest, msg: "provider returned status 400: " + body},
			"OpenAI did not accept this key (error 400). Check the key and the endpoint address."},
		{"unclassified", errors.New(`parse "::bad": missing protocol scheme`),
			"Could not check this key with OpenAI. Check the endpoint address and try again."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keyCheckReason(ProviderOpenAI, tc.err)
			if got != tc.want {
				t.Errorf("keyCheckReason = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "Internal server error") || strings.Contains(got, "{") {
				t.Errorf("message quotes the provider body: %q", got)
			}
		})
	}
}

func TestKeyCheckReasonUsesDisplayName(t *testing.T) {
	got := keyCheckReason(ProviderGoogle, &callFailure{status: http.StatusUnauthorized})
	if !strings.HasPrefix(got, "Google Translate rejected") {
		t.Errorf("want the vendor's display name, got %q", got)
	}
}
