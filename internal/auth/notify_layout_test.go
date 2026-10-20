package auth

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
)

type capturingSender struct{ bodies []string }

func (c *capturingSender) SendEmail(_ context.Context, _, _ string, body emaillayout.Email) error {
	c.bodies = append(c.bodies, body.HTML())
	return nil
}

// TestAccountEmailsUseSharedLayout — every account email goes through the
// shared layout with the logo from the app's origin, its button opens the
// right page, and the org/user names in it are HTML-escaped.
func TestAccountEmailsUseSharedLayout(t *testing.T) {
	const evilOrg = `Acme <b>Evil</b>`
	domain := "example.com"
	ctx := context.Background()

	cases := map[string]struct {
		send func(h *Handler)
		// button is the call to action's href; empty means the email has none.
		button string
	}{
		"signup-operator-notice": {
			send: func(h *Handler) {
				h.notifyOperatorsOfSignup(ctx,
					db.Tenant{Name: evilOrg, Domain: &domain},
					db.User{Name: `Jo <i>`, Email: "owner@test.com"})
			},
			button: "https://app.example.com/platform/orgs",
		},
		"owner-invite": {
			send:   func(h *Handler) { h.notifyInvitedOwner(ctx, "owner@test.com", evilOrg, "tok123") },
			button: "https://app.example.com/invite/tok123",
		},
		"signup-approved": {
			send:   func(h *Handler) { h.notifyOwnerApproved(ctx, "owner@test.com", evilOrg) },
			button: "https://app.example.com/",
		},
		"org-disabled":    {send: func(h *Handler) { h.notifyOwnerDisabled(ctx, "owner@test.com", evilOrg) }},
		"signup-rejected": {send: func(h *Handler) { h.notifyOwnerRejected(ctx, "owner@test.com", evilOrg) }},
		"password-reset": {
			send:   func(h *Handler) { h.sendPasswordResetEmail(ctx, "owner@test.com", "tok123") },
			button: "https://app.example.com/reset-password/tok123",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &capturingSender{}
			h := &Handler{
				sender:         fake,
				baseURL:        "https://app.example.com",
				platformAdmins: map[string]bool{"ops@test.com": true},
			}
			tc.send(h)
			if len(fake.bodies) != 1 {
				t.Fatalf("expected 1 email, got %d", len(fake.bodies))
			}
			body := fake.bodies[0]
			if !strings.Contains(body, emaillayout.HeaderMarker) {
				t.Errorf("email does not use the shared layout:\n%s", body)
			}
			if !strings.Contains(body, `src="https://app.example.com`+emaillayout.LogoPath+`"`) {
				t.Errorf("logo does not load from the app's origin:\n%s", body)
			}
			if tc.button == "" {
				if strings.Contains(body, `bgcolor="#155dfc"`) {
					t.Errorf("unexpected button:\n%s", body)
				}
			} else if !strings.Contains(body, `<a href="`+tc.button+`" style="display: inline-block;`) {
				t.Errorf("button does not open %s:\n%s", tc.button, body)
			}
			if strings.Contains(body, "<b>Evil</b>") || strings.Contains(body, "<i>") {
				t.Errorf("org/user name not escaped:\n%s", body)
			}
		})
	}
}
