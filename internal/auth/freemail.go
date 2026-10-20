package auth

import "strings"

// freeEmailDomains is the blocklist of public/personal email providers. A
// person signing in with one of these can join an org by invite, but cannot
// create a *new* org from it — a "gmail" org derived from
// john@gmail.com is meaningless, and it would poison the domain→org auto-join
// and "open signup for @domain" mechanics. Keep lowercase; matched exactly.
var freeEmailDomains = map[string]bool{
	"gmail.com":       true,
	"googlemail.com":  true,
	"outlook.com":     true,
	"hotmail.com":     true,
	"hotmail.co.uk":   true,
	"live.com":        true,
	"msn.com":         true,
	"yahoo.com":       true,
	"yahoo.co.uk":     true,
	"ymail.com":       true,
	"icloud.com":      true,
	"me.com":          true,
	"mac.com":         true,
	"aol.com":         true,
	"proton.me":       true,
	"protonmail.com":  true,
	"pm.me":           true,
	"gmx.com":         true,
	"gmx.net":         true,
	"mail.com":        true,
	"zoho.com":        true,
	"yandex.com":      true,
	"yandex.ru":       true,
	"qq.com":          true,
	"163.com":         true,
	"126.com":         true,
	"fastmail.com":    true,
	"hey.com":         true,
	"tutanota.com":    true,
}

// IsFreeEmailDomain reports whether a domain belongs to a public/personal email
// provider. Case-insensitive; a leading "@" is tolerated.
func IsFreeEmailDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(domain, "@")))
	return freeEmailDomains[d]
}

// emailDomain returns the lowercased domain part of an email, or "" if the
// email isn't in a parseable "local@domain" shape.
func emailDomain(email string) string {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(email)), "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return ""
	}
	return parts[1]
}
