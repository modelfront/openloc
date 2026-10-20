package auth

import "testing"

func TestUserLabel(t *testing.T) {
	cases := []struct {
		name  string
		uName string
		email string
		want  string
	}{
		{"real name and email", "Test User", "test@test.com", "Test User <test@test.com>"},
		{"name equals email collapses to email", "dev@example.com", "dev@example.com", "dev@example.com"},
		{"empty name falls back to email", "", "dev@example.com", "dev@example.com"},
		{"empty email falls back to name", "Jane", "", "Jane"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserLabel(tc.uName, tc.email); got != tc.want {
				t.Errorf("UserLabel(%q, %q) = %q, want %q", tc.uName, tc.email, got, tc.want)
			}
		})
	}
}
