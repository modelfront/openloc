package auth

import "sort"

// EmailSet is a set of email addresses compared the way sign-in compares them
// (normalizeEmail): the platform operators of PLATFORM_ADMIN_EMAILS.
type EmailSet map[string]bool

// NewEmailSet holds the given addresses; blank ones are dropped.
func NewEmailSet(emails []string) EmailSet {
	s := make(EmailSet, len(emails))
	for _, e := range emails {
		if e = normalizeEmail(e); e != "" {
			s[e] = true
		}
	}
	return s
}

// Has reports whether email is in the set, whatever its case and spacing.
func (s EmailSet) Has(email string) bool { return s[normalizeEmail(email)] }

// Emails is the set's addresses, normalized and sorted.
func (s EmailSet) Emails() []string {
	out := make([]string, 0, len(s))
	for e := range s {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
