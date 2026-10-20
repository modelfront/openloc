package errtrack_test

import (
	"errors"
	"testing"

	"openloc/internal/errtrack"
)

func TestSendFailureRedactsAddresses(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"gmail: RCPT TO invitee@customer.example: 550 mailbox unavailable", "gmail: RCPT TO " + errtrack.Redacted + ": 550 mailbox unavailable"},
		{"from a.b+c@x.example to d@y.example failed", "from " + errtrack.Redacted + " to " + errtrack.Redacted + " failed"},
		// Invites accept any address, so the scrubber can't assume ASCII or a
		// plain local part.
		{"RCPT TO josé@customer.example: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"RCPT TO user@bücher.example: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"RCPT TO <用户@例子.example>: 550", "RCPT TO <" + errtrack.Redacted + ">: 550"},
		{"RCPT TO mary.o'connor@customer.example: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"RCPT TO a`b@customer.example: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"RCPT TO <\"john doe\"@customer.example>: 550", "RCPT TO <" + errtrack.Redacted + ">: 550"},
		{"RCPT TO user@[192.0.2.1]: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"RCPT TO user@[IPv6:2001:db8::1]: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		// 例え.テスト, IANA's reserved IDN test domain: a TLD with digits and hyphens.
		{"RCPT TO user@xn--r8jz45g.xn--zckzah: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		// Invites accept addresses without a dot in the domain.
		{"550 5.1.1 <john@acme>: Recipient address rejected", "550 5.1.1 <" + errtrack.Redacted + ">: Recipient address rejected"},
		{"RCPT TO john@localhost: 550", "RCPT TO " + errtrack.Redacted + ": 550"},
		{"dial tcp: connection refused", "dial tcp: connection refused"},
		{"535 5.7.8 Username and Password not accepted", "535 5.7.8 Username and Password not accepted"},
	} {
		if got := errtrack.SendFailure(errors.New(tc.in), "").Error(); got != tc.want {
			t.Errorf("SendFailure(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A server can quote a recipient the address pattern cannot find, so the
// recipient itself is removed, whatever its case.
func TestSendFailureRedactsTheRecipient(t *testing.T) {
	for _, tc := range []struct{ in, recipient, want string }{
		{"553 5.1.3 The recipient address <john.doe> is not a valid RFC-5321 address", "john.doe",
			"553 5.1.3 The recipient address <" + errtrack.Redacted + "> is not a valid RFC-5321 address"},
		{"553 5.1.3 <John.Doe> rejected", "john.doe", "553 5.1.3 <" + errtrack.Redacted + "> rejected"},
		{"501 5.1.3 Bad recipient address syntax: <john@>", " john@ ", "501 5.1.3 Bad recipient address syntax: <" + errtrack.Redacted + ">"},
		{"550 5.1.1 <a.b+c@x.example>: unknown", "a.b+c@x.example", "550 5.1.1 <" + errtrack.Redacted + ">: unknown"},
	} {
		if got := errtrack.SendFailure(errors.New(tc.in), tc.recipient).Error(); got != tc.want {
			t.Errorf("SendFailure(%q, %q) = %q, want %q", tc.in, tc.recipient, got, tc.want)
		}
	}
}
