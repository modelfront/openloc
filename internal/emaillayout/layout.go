// Package emaillayout renders the one HTML shell every outgoing email uses:
// a navy brand bar, then a white card with a heading, body, optional details
// table, button and small print, and a footer. Callers build the parts with
// P, Note, Callout, Link, Strong and Text, which escape every value they are
// given, so user-supplied text (org names, user names) can't inject markup.
//
// The markup is email-safe: tables for layout and inline styles only, because
// mail clients drop <style> blocks and most CSS layout.
//
// Leaf package on purpose — auth, invite and notification all import it, and
// notification already imports auth.
package emaillayout

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// HeaderMarker is the brand bar markup every email starts with. Tests in
// other packages use it to check an email went through Render.
const HeaderMarker = `>OpenLoc</td>`

// LogoPath is the logo mark in the frontend's public folder, served from the
// app's origin. Mail clients don't show SVG, so it is a PNG; the wordmark next
// to it is text, so the bar still reads "OpenLoc" when images are blocked.
const LogoPath = "/email-logo.png"

// Content is one email. Body and Notes are trusted HTML — build them with the
// helpers below.
type Content struct {
	// Preheader is the hidden line an inbox list shows after the subject.
	Preheader string
	Heading   string
	Body      template.HTML
	// Details is a label/value table under the body.
	Details []Row
	// Button is the call to action under the details.
	Button *Button
	// Notes is the small print under the button, built with Note and LinkFallback.
	Notes template.HTML
	// Footer replaces the default footer line. Build it with Text.
	Footer template.HTML
}

// Row is one line of the details table.
type Row struct {
	Label string
	Value string
}

// Button is a call to action.
type Button struct {
	Label string
	URL   string
}

// Inter is the app's font. Mail clients rarely load web fonts, so the stack
// falls through to each platform's own sans-serif.
const fontStack = `Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif`

const defaultFooter = template.HTML("Sent by OpenLoc.")

var layoutTmpl = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
</head>
<body style="margin: 0; padding: 0; background-color: #eef0f3;">
{{if .Preheader}}<div style="display: none; max-height: 0; overflow: hidden; mso-hide: all;">{{.Preheader}}</div>{{end}}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#eef0f3" style="background-color: #eef0f3;">
<tr><td align="center" style="padding: 28px 16px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width: 560px; background-color: #ffffff; border: 1px solid #e4e7ec; border-radius: 10px; font-family: ` + fontStack + `;">
<tr><td bgcolor="#101828" style="background-color: #101828; padding: 18px 32px; border-radius: 10px 10px 0 0;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
{{if .LogoURL}}<td style="padding-right: 10px; vertical-align: middle;"><img src="{{.LogoURL}}" width="24" height="24" alt="" style="display: block; border: 0;"></td>{{end}}
<td style="vertical-align: middle; font-family: ` + fontStack + `; font-size: 16px; font-weight: 600; letter-spacing: -0.01em; color: #ffffff;">OpenLoc</td>
</tr></table>
</td></tr>
<tr><td style="padding: 32px;">
<h1 style="margin: 0 0 12px; font-family: ` + fontStack + `; font-size: 22px; font-weight: 600; line-height: 1.3; color: #101828;">{{.Heading}}</h1>
{{.Body}}
{{- if .Details}}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin-top: 18px; border-collapse: collapse; font-family: ` + fontStack + `; font-size: 14px;">
{{- range $i, $r := .Details}}
<tr><td width="120" style="padding: 9px 12px 9px 0; vertical-align: top; color: #667085;{{if $i}} border-top: 1px solid #f2f4f7;{{end}}">{{$r.Label}}</td><td style="padding: 9px 0; color: #101828;{{if $i}} border-top: 1px solid #f2f4f7;{{end}}">{{$r.Value}}</td></tr>
{{- end}}
</table>
{{- end}}
{{- with .Button}}
<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin-top: 24px;"><tr><td bgcolor="#155dfc" style="background-color: #155dfc; border-radius: 6px;"><a href="{{.URL}}" style="display: inline-block; padding: 11px 20px; font-family: ` + fontStack + `; font-size: 14px; font-weight: 600; color: #ffffff; text-decoration: none; border-radius: 6px;">{{.Label}}</a></td></tr></table>
{{- end}}
{{.Notes}}
</td></tr>
<tr><td bgcolor="#f9fafb" style="background-color: #f9fafb; border-top: 1px solid #eaecf0; padding: 16px 32px; border-radius: 0 0 10px 10px; font-family: ` + fontStack + `; font-size: 12px; line-height: 1.5; color: #98a2b3;">{{.Footer}}</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`))

type view struct {
	Content
	LogoURL string
}

// Email is a rendered email body. Only Render makes one, and every sender
// takes an Email, so an email that skips the layout does not compile.
type Email struct {
	html   string
	thread Thread
}

// HTML is the email's markup, for the transport.
func (e Email) HTML() string {
	return e.html
}

// Thread puts the emails about one thing in one conversation in mail apps.
// ID is stable for the thing ("issue-42.app.example.com") and becomes the
// message id that every email of the thread refers to; Root marks the email
// that starts the thread and carries that id itself. The zero Thread is no
// thread.
type Thread struct {
	ID   string
	Root bool
}

// InThread is e as an email of thread t.
func (e Email) InThread(t Thread) Email {
	e.thread = t
	return e
}

// Thread is the thread e belongs to, for the transport.
func (e Email) Thread() Thread {
	return e.thread
}

// AppURL is path on the app's origin baseURL, for a link or image in an email.
// It is empty when there is no origin: a relative URL opens nowhere in a mail
// client.
func AppURL(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return ""
	}
	return base + path
}

// Render wraps c in the shared layout. baseURL is the app's origin, which
// serves the logo; without it the bar shows the wordmark alone.
func Render(baseURL string, c Content) (Email, error) {
	v := view{Content: c, LogoURL: AppURL(baseURL, LogoPath)}
	if v.Footer == "" {
		v.Footer = defaultFooter
	}
	var buf bytes.Buffer
	if err := layoutTmpl.Execute(&buf, v); err != nil {
		return Email{}, fmt.Errorf("render email layout: %w", err)
	}
	return Email{html: buf.String()}, nil
}

// P builds a body paragraph. format is trusted markup; every arg is
// HTML-escaped unless it is already template.HTML (from Link or Strong).
func P(format string, args ...any) template.HTML {
	return wrap(`<p style="margin: 0 0 12px; font-family: `+fontStack+`; font-size: 15px; line-height: 1.6; color: #475467;">`, format, args, `</p>`)
}

// Note builds a line of small print, such as when a link expires.
func Note(format string, args ...any) template.HTML {
	return wrap(`<p style="margin: 20px 0 0; font-family: `+fontStack+`; font-size: 13px; line-height: 1.5; color: #667085;">`, format, args, `</p>`)
}

// LinkFallback prints url as text under a button, for mail apps where the
// button does not open.
func LinkFallback(url string) template.HTML {
	return template.HTML(`<p style="margin: 10px 0 0; font-family: ` + fontStack + `; font-size: 12px; line-height: 1.5; color: #98a2b3; word-break: break-all;">Button not working? Paste this link into your browser:<br><span style="color: #667085;">` +
		template.HTMLEscapeString(url) + `</span></p>`)
}

// Callout builds a boxed note a person wrote — a reason, a comment, an issue's
// description or reply — with a small label above it, keeping its line breaks.
func Callout(label, text string) template.HTML {
	return template.HTML(`<div style="margin: 16px 0; padding: 12px 14px; background-color: #f9fafb; border: 1px solid #eaecf0; border-radius: 6px; font-family: ` + fontStack + `;">` +
		`<div style="margin-bottom: 4px; font-size: 12px; color: #667085;">` + template.HTMLEscapeString(label) + `</div>` +
		`<div style="font-size: 14px; line-height: 1.5; color: #101828; white-space: pre-line;">` + template.HTMLEscapeString(text) + `</div></div>`)
}

// Link builds an anchor with an escaped href and text.
func Link(href, text string) template.HTML {
	return template.HTML(`<a href="` + template.HTMLEscapeString(href) + `" style="color: #155dfc;">` + template.HTMLEscapeString(text) + `</a>`)
}

// Strong builds escaped bold text.
func Strong(text string) template.HTML {
	return template.HTML(`<strong style="font-weight: 600; color: #101828;">` + template.HTMLEscapeString(text) + `</strong>`)
}

// Text builds inline text with no wrapping tag, for the footer.
func Text(format string, args ...any) template.HTML {
	return wrap("", format, args, "")
}

// Join concatenates body parts.
func Join(parts ...template.HTML) template.HTML {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(string(p))
	}
	return template.HTML(b.String())
}

func wrap(open, format string, args []any, closeTag string) template.HTML {
	escaped := make([]any, len(args))
	for i, a := range args {
		if h, ok := a.(template.HTML); ok {
			escaped[i] = h
		} else {
			escaped[i] = template.HTMLEscapeString(fmt.Sprint(a))
		}
	}
	return template.HTML(open + fmt.Sprintf(format, escaped...) + closeTag)
}
