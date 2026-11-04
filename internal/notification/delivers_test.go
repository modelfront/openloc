package notification

import "testing"

// Only a sender that reaches people counts: the console fallback, which a
// server uses when no email is configured, only logs.
func TestDelivers(t *testing.T) {
	cases := []struct {
		name string
		s    Sender
		want bool
	}{
		{"nil", nil, false},
		{"console fallback", &ConsoleSender{}, false},
		{"smtp", &SMTPSender{}, true},
		{"dev outbox", &RecordingSender{Next: &ConsoleSender{}, Outbox: NewOutbox(1)}, true},
	}
	for _, c := range cases {
		if got := Delivers(c.s); got != c.want {
			t.Errorf("%s: Delivers = %v, want %v", c.name, got, c.want)
		}
	}
}
