package langcode

import (
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Label names a language the way an LLM prompt should carry it: the English
// name followed by the canonical code — "Austrian German (de-AT)".
//
// Both halves earn their place. A bare code leaves the model to guess whether
// "de-at" is a language, a country or a typo. A bare name is ambiguous the
// other way: "German" says nothing about which variant the job wants, which is
// the whole point of running de-AT rather than de.
//
// The name comes from CLDR, so it is the one an English-speaking reviewer would
// recognize. The code is re-rendered in canonical BCP 47 casing ("de-AT", not
// the lowercase form the database stores) because that is the form the model
// has seen everywhere else.
//
// A code CLDR does not recognize — including the Wildcard sentinel — comes back
// unchanged. Naming it is a nicety; passing it through is what keeps the prompt
// correct.
func Label(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	name := display.English.Languages().Name(tag)
	if name == "" {
		return code
	}
	return name + " (" + tag.String() + ")"
}

// BCP47 renders code in canonical BCP 47 casing — "es-MX", "zh-Hans" — the
// form documents such as Office files write, rather than the lowercase form
// the database stores. A code that does not parse comes back unchanged.
func BCP47(code string) string {
	code = strings.TrimSpace(code)
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	return tag.String()
}

// Known reports a code that names a language CLDR knows, such as "id" or
// "pt-BR". A short word that merely parses as a code ("sku", "qty") does not.
func Known(code string) bool {
	tag, err := language.Parse(strings.TrimSpace(code))
	return err == nil && display.English.Languages().Name(tag) != ""
}
