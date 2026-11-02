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

// LabelName is what a caller who may not see colleagues' emails gets: it must
// never return an email, and must leave a label that has none alone.
func TestLabelName(t *testing.T) {
	cases := []struct{ name, label, want string }{
		{"name and email keeps the name", UserLabel("Test User", "test@test.com"), "Test User"},
		{"bare email is dropped", UserLabel("", "dev@example.com"), ""},
		{"name only stays", UserLabel("Jane", ""), "Jane"},
		{"tmx creation id stays", "translator01", "translator01"},
		{"email beside a name is dropped", "Jane Doe jane@x.com", ""},
		{"email in brackets is dropped", "Jane (jane@x.com)", ""},
		{"email with a leading space is dropped", " jane@x.com", ""},
		{"spaces around a name are trimmed", " Jane <jane@x.com> ", "Jane"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LabelName(tc.label); got != tc.want {
				t.Errorf("LabelName(%q) = %q, want %q", tc.label, got, tc.want)
			}
		})
	}
}
