package auth

import (
	"slices"
	"testing"
)

func TestEmailSet(t *testing.T) {
	s := NewEmailSet([]string{" Ops@Example.com ", "", "b@example.com"})
	if !s.Has("ops@example.com") || !s.Has("  OPS@example.COM") || s.Has("other@example.com") || s.Has("") {
		t.Errorf("Has: %v", s)
	}
	if got := s.Emails(); !slices.Equal(got, []string{"b@example.com", "ops@example.com"}) {
		t.Errorf("Emails = %v", got)
	}
}
