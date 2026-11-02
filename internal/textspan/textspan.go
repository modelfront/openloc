// Package textspan holds the patterns for the parts of a text that are not
// prose: web addresses, email addresses and the variables of software
// strings. The QA number checks skip them and the placeable finder offers
// them for insertion.
package textspan

import "regexp"

const (
	urlPattern   = `\b(?:https?://|www\.)[^\s<>"]+`
	emailPattern = `[\w.+-]+@[\w-]+(?:\.[\w-]+)+`
	// "{0}", "{{count}}", "${name}" and printf's "%1$s" / "%d". The printf part
	// takes no space flag: "20% discount" is a percentage, not "% d".
	variablePattern = `\{\{?[^{}\s]{1,40}\}?\}` +
		`|\$\{[^{}\s]{1,40}\}` +
		`|%(?:\d+\$)?[-+#0]?\d*(?:\.\d+)?[sdfiuxXeEgGc@]`
)

var (
	// URL matches a web address starting with a scheme or "www.".
	URL = regexp.MustCompile(`(?i)` + urlPattern)
	// Email matches an email address.
	Email = regexp.MustCompile(emailPattern)
	// Variable matches a software placeholder.
	Variable = regexp.MustCompile(variablePattern)
	// Any matches any of the three.
	Any = regexp.MustCompile(`(?i)` + urlPattern + `|` + emailPattern + `|` + variablePattern)
)

// TrimURL drops the punctuation that ends a sentence from a matched web
// address: in "see www.example.com." the dot is not part of it.
func TrimURL(s string) string {
	for len(s) > 0 {
		switch s[len(s)-1] {
		case '.', ',', ';', ':', '!', '?', ')', ']', '\'':
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}
