package auth_test

import (
	"testing"

	"openloc/internal/auth"
)

func TestRoleAtLeast(t *testing.T) {
	for _, tc := range []struct {
		role, other string
		want        bool
	}{
		{auth.RoleOwner, auth.RoleAdmin, true},
		{auth.RoleOwner, auth.RoleOwner, true},
		{auth.RoleAdmin, auth.RoleAdmin, true},
		{auth.RoleAdmin, auth.RoleOwner, false},
		{auth.RoleProjectManager, auth.RoleLinguist, true},
		{auth.RoleLinguist, auth.RoleProjectManager, false},
		// An unknown role on either side never passes.
		{"", auth.RoleLinguist, false},
		{"vendor", auth.RoleLinguist, false},
		{auth.RoleOwner, "vendor", false},
		{auth.RoleOwner, "", false},
	} {
		if got := auth.RoleAtLeast(tc.role, tc.other); got != tc.want {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", tc.role, tc.other, got, tc.want)
		}
	}
}
