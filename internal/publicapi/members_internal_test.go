package publicapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"openloc/internal/invite"
)

// A refused invite keeps its status; a spent email budget answers like every
// other 429: code rate_limited and a Retry-After.
func TestWriteInviteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeInviteError(rec, invite.RuleError{Status: http.StatusTooManyRequests, Msg: "slow down", RetryAfter: 30 * time.Second}, "create")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("budget refusal = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "rate_limited" {
		t.Fatalf("code = %q, want rate_limited", body.Error.Code)
	}

	rec = httptest.NewRecorder()
	writeInviteError(rec, invite.RuleError{Status: http.StatusForbidden, Msg: "linguists only"}, "create")
	if rec.Code != http.StatusForbidden || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("rule refusal = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}
