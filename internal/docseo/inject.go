package docseo

import (
	"bytes"
	"errors"
	"html/template"
	"regexp"
)

var (
	titleRe = regexp.MustCompile(`<title>[^<]*</title>`)
	rootRe  = regexp.MustCompile(`<div id="root">\s*</div>`)
)

var headTmpl = template.Must(template.New("head").Parse(`<title>{{.Title}}</title>
    <meta name="description" content="{{.Description}}" />
    <link rel="canonical" href="{{.Canonical}}" />
    <meta property="og:type" content="website" />
    <meta property="og:title" content="{{.Title}}" />
    <meta property="og:description" content="{{.Description}}" />
    <meta property="og:url" content="{{.Canonical}}" />
    <style>[data-prerendered] { display: none; }</style>`))

// Inject puts the page into the app's index.html: its title and meta tags in
// the head, its body inside the root element, which the app replaces when it
// starts. The body is hidden in a browser, which shows the app instead; readers
// that take the HTML as text still get it. An index without an empty root element or a title is an error:
// the page would go out without its content.
func Inject(index []byte, p Page) ([]byte, error) {
	if !titleRe.Match(index) || !rootRe.Match(index) {
		return nil, errors.New("docseo: index.html has no <title> or empty <div id=\"root\">")
	}
	var head bytes.Buffer
	if err := headTmpl.Execute(&head, p); err != nil {
		return nil, err
	}
	out := titleRe.ReplaceAllLiteral(index, head.Bytes())
	root := append(append([]byte(`<div id="root"><div data-prerendered>`), p.Body...), "</div></div>"...)
	return rootRe.ReplaceAllLiteral(out, root), nil
}
