package parser

import (
	"fmt"
	"strings"
)

// HTMLOptions are the import options of an HTML file. A file exports with the
// options it was imported with: they decide which segments exist and their keys.
type HTMLOptions struct {
	// Attributes names the attributes translated per element, by element name
	// (any name, custom elements included). An input of a type in
	// htmlInputNoText never has its attributes translated.
	Attributes map[string][]string `json:"attributes"`
	// MetaDescription translates <meta name="description" content="...">.
	MetaDescription bool `json:"meta_description"`
	// SkipPre leaves <pre> untranslated. When false, its text is translated
	// with its whitespace kept as is.
	SkipPre bool `json:"skip_pre"`
}

// DefaultHTMLOptions are the options of an HTML file with none chosen.
func DefaultHTMLOptions() HTMLOptions {
	return HTMLOptions{
		Attributes: map[string][]string{
			"img":   {"alt", "title"},
			"a":     {"title"},
			"abbr":  {"title"},
			"input": {"alt", "value", "title", "placeholder"},
			"td":    {"abbr"},
			"th":    {"abbr"},
		},
		MetaDescription: true,
		SkipPre:         true,
	}
}

// Validate rejects an attribute that is not plain text for a reader
// (htmlTextAttr), and any attribute of an element that configures the page
// (htmlConfigElements): a translator sets the attribute's value in the
// exported file, so it must never be a URL, a script, a style or a directive.
func (o HTMLOptions) Validate() error {
	for elem, attrs := range o.Attributes {
		if strings.TrimSpace(elem) == "" {
			return fmt.Errorf("html options: empty element name")
		}
		if htmlConfigElements[strings.ToLower(strings.TrimSpace(elem))] {
			return fmt.Errorf("html options: attributes of %q cannot be translated", elem)
		}
		for _, a := range attrs {
			if strings.TrimSpace(a) == "" {
				return fmt.Errorf("html options: empty attribute name on %q", elem)
			}
			if !htmlTextAttr(elem, a) {
				return fmt.Errorf("html options: attribute %q on %q cannot be translated", a, elem)
			}
		}
	}
	return nil
}

// htmlTextAttrs hold text a reader sees or hears. An allowlist: a blocklist of
// URL and script attributes always misses one (ping, manifest, http-equiv…),
// and data-* attributes are left out because page scripts read them as code
// or URLs (data-bind, data-src).
var htmlTextAttrs = map[string]bool{
	"alt": true, "title": true, "placeholder": true, "abbr": true,
	"label": true, "summary": true, "aria-label": true, "aria-description": true,
	"aria-placeholder": true, "aria-roledescription": true, "aria-valuetext": true,
}

// htmlConfigElements configure the page rather than show text.
var htmlConfigElements = map[string]bool{"meta": true, "link": true, "base": true, "html": true, "head": true}

// htmlTextAttr reports whether a translator may set the attribute on elem: a
// text attribute, or value on an input, where it is the button or field
// text. On other elements value is form data or a URL (option, param).
func htmlTextAttr(elem, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return htmlTextAttrs[name] || (name == "value" && strings.ToLower(strings.TrimSpace(elem)) == "input")
}

// htmlConfig is HTMLOptions ready for a parse: names lower-cased, attributes
// Validate refuses dropped.
type htmlConfig struct {
	attrs   map[string][]string
	meta    bool
	skipPre bool
}

var defaultHTMLConfig = DefaultHTMLOptions().config()

func (p HTML) config() htmlConfig {
	if p.Options == nil {
		return defaultHTMLConfig
	}
	return p.Options.config()
}

func (o HTMLOptions) config() htmlConfig {
	attrs := make(map[string][]string, len(o.Attributes))
	for elem, names := range o.Attributes {
		elem = strings.ToLower(strings.TrimSpace(elem))
		for _, a := range names {
			a = strings.ToLower(strings.TrimSpace(a))
			if a != "" && htmlTextAttr(elem, a) && !htmlConfigElements[elem] && !containsString(attrs[elem], a) {
				attrs[elem] = append(attrs[elem], a)
			}
		}
	}
	return htmlConfig{attrs: attrs, meta: o.MetaDescription, skipPre: o.SkipPre}
}
