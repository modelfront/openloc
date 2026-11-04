package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func gateCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return env.Error.Code
}

func TestVendorRequestAllowed(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/auth/dev-role", true},
		{"GET", "/api/members", true},
		{"GET", "/api/members/page", true},
		{"GET", "/api/members/facets", true},
		{"GET", "/api/invites/accepted", true},
		{"PUT", "/api/members/7/role", true},
		{"DELETE", "/api/members/7", true},
		{"POST", "/api/members", false},
		{"GET", "/api/invites", true},
		{"GET", "/api/invites/", true},
		{"POST", "/api/invites/", true},
		{"DELETE", "/api/invites/3", true},
		{"POST", "/api/invites/3/resend", true},
		{"GET", "/api/users/7/language-pairs", true},
		{"PUT", "/api/users/7/language-pairs", true},
		{"GET", "/api/users/by-language-pair", false},
		{"GET", "/api/languages", true},
		{"POST", "/api/languages", false},
		{"GET", "/api/tasks", true},
		{"GET", "/api/tasks/steps", true},
		{"POST", "/api/tasks", false},
		{"GET", "/api/projects", true},
		{"GET", "/api/projects/", true},
		{"POST", "/api/projects/", false},
		{"GET", "/api/projects/1", false},
		{"GET", "/api/projects/1/tasks", false},
		{"GET", "/api/notifications/", true},
		{"GET", "/api/notifications/count", true},
		{"GET", "/api/notifications/stream", true},
		{"POST", "/api/notifications/read-all", true},
		{"PATCH", "/api/notifications/4/read", true},
		{"GET", "/api/notification-preferences", true},
		{"PUT", "/api/notification-preferences", true},
		{"GET", "/api/appearance", true},
		{"PUT", "/api/appearance", true},
		{"GET", "/api/docs/", true},
		{"GET", "/api/docs/guides/customers-and-vendors", true},
		{"GET", "/api/api-docs/openapi.json", true},
		{"GET", "/api/issues", true},
		{"POST", "/api/issues", true},
		{"PATCH", "/api/issues/2/kind", true},
		{"GET", "/api/issues/2/attachments/3", true},
		{"POST", "/mcp", false},
		{"GET", "/mcp", false},
		{"POST", "/api/chat/", false},
		{"GET", "/api/chat/models", false},
		{"GET", "/api/v1/projects/", false},
		{"GET", "/api/tms", false},
		{"GET", "/api/org/settings", false},
		{"GET", "/api/api-tokens/", false},
		{"GET", "/api/membersx", false},
		{"GET", "/api/tasksx", false},
		// The check reads the path chi routes on, so an encoded twin of an
		// allowed path is refused rather than judged by its decoded form.
		{"GET", "/api/%74asks", false},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if got := vendorRequestAllowed(req); got != tc.want {
			t.Errorf("%s %s allowed = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestCheckTenantActiveVendorOrg(t *testing.T) {
	gate := func(g TenantGate) TenantActive {
		return func(context.Context, int64, int64) (TenantGate, error) { return g, nil }
	}
	vendor := TenantGate{Kind: TenantKindVendor, ClientActive: true}
	for _, tc := range []struct {
		name     string
		gate     TenantGate
		method   string
		path     string
		wantOK   bool
		wantCode string
	}{
		{"vendor on the allow-list", vendor, "GET", "/api/tasks", true, ""},
		{"vendor off the allow-list", vendor, "POST", "/api/tms", false, "vendor_org_restricted"},
		{"vendor on mcp", vendor, "POST", "/mcp", false, "vendor_org_restricted"},
		{"inactive client, allowed route", TenantGate{Kind: TenantKindVendor}, "GET", "/api/tasks", false, "client_inactive"},
		{"inactive client, restricted route", TenantGate{Kind: TenantKindVendor}, "POST", "/api/tms", false, "client_inactive"},
		{"customer org", TenantGate{Kind: TenantKindCustomer}, "POST", "/api/tms", true, ""},
		{"customer org on mcp", TenantGate{Kind: TenantKindCustomer}, "POST", "/mcp", true, ""},
		{"demo read", TenantGate{Kind: TenantKindDemo}, "GET", "/api/tms", true, ""},
		{"demo write", TenantGate{Kind: TenantKindDemo}, "POST", "/api/tms", false, "demo_read_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			_, ok := checkTenantActive(rec, req, gate(tc.gate), 1, 2, false)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (%d %s)", ok, tc.wantOK, rec.Code, rec.Body.String())
			}
			if tc.wantOK {
				return
			}
			if rec.Code != http.StatusForbidden || gateCode(t, rec) != tc.wantCode {
				t.Errorf("answer = %d %s, want 403 %s", rec.Code, rec.Body.String(), tc.wantCode)
			}
		})
	}
}
