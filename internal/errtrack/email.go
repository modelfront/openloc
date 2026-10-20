package errtrack

import (
	"errors"
	"regexp"
	"strings"
)

// emailAddressRE matches an email address in free text. The local part is any
// letters plus the RFC 5322 atom characters, apostrophe and backtick included
// (mary.o'connor@…), or a quoted string ("john doe"@…). The domain is any
// letters (user@bücher.example), with or without a dot, since an invite
// accepts any text (user@acme), or an IP literal (user@[192.0.2.1]).
var emailAddressRE = regexp.MustCompile(`(?:"[^"\r\n]+"|[\p{L}\p{N}.!#$%&'*+/=?^_\x60{|}~\-]+)@(?:\[[^\]\s]+\]|(?:[\p{L}\p{N}\-]+\.)*[\p{L}\p{N}\-]+)`)

// SendFailure is the error a notice gets for a failed email: the provider's
// message with the recipient, in any case, and every other email address in
// it replaced by Redacted. The message tells a refused connection from a
// rejected credential; a server's reply can quote the recipient, and that must
// not leave our infrastructure. The recipient is removed as given first,
// because a malformed one (<john.doe>) is no address the pattern can find.
// Log the full error.
func SendFailure(err error, recipient string) error {
	msg := err.Error()
	if r := strings.TrimSpace(recipient); r != "" {
		msg = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(r)).ReplaceAllString(msg, Redacted)
	}
	return errors.New(emailAddressRE.ReplaceAllString(msg, Redacted))
}
