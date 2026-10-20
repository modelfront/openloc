package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openloc/internal/auth"
)

// The chat refuses a read-only access key itself, without relying on
// auth.Middleware. A zero Handler proves the refusal comes before any
// dependency is touched.
func TestChatRefusesReadOnlyKey(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		name string
		body string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"send", `{"message":"delete everything","allow_writes":true}`, h.HandleSend},
		{"confirm", `{"conversation_id":1}`, h.HandleConfirm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := auth.WithClaims(t.Context(), &auth.Claims{UserID: 1, TenantID: 1, Role: "owner"})
			ctx = auth.WithPATInfo(ctx, &auth.PATInfo{Capability: auth.CapabilityRead})
			req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(c.body)).WithContext(ctx)
			rec := httptest.NewRecorder()
			c.call(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"read_only_key"`) {
				t.Fatalf("status = %d body = %s, want 403 read_only_key", rec.Code, rec.Body.String())
			}
		})
	}
}
