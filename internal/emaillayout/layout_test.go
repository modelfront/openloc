package emaillayout

import (
	"strings"
	"testing"
)

func render(t *testing.T, baseURL string, c Content) string {
	t.Helper()
	email, err := Render(baseURL, c)
	if err != nil {
		t.Fatal(err)
	}
	out := email.HTML()
	if strings.Contains(out, "ZgotmplZ") {
		t.Fatalf("html/template rejected a value:\n%s", out)
	}
	return out
}

func TestRender_WrapsContentInLayout(t *testing.T) {
	out := render(t, "https://app.example.com", Content{
		Preheader: "Preview line",
		Heading:   "Hello",
		Body:      P("Body text"),
		Details:   []Row{{Label: "Project", Value: "Launch"}, {Label: "File", Value: "notes.docx"}},
		Button:    &Button{Label: "Open job", URL: "https://app.example.com/projects/1/jobs/2/edit"},
		Notes:     Note("Expires soon."),
		Footer:    Text("Footer text"),
	})
	for _, want := range []string{
		"<!DOCTYPE html>", HeaderMarker, "Preview line", "<h1", "Hello", "Body text",
		">Project</td>", ">Launch</td>", ">File</td>", ">notes.docx</td>",
		`href="https://app.example.com/projects/1/jobs/2/edit"`, ">Open job</a>",
		"Expires soon.", "Footer text", "</html>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRender_NavyBrandBarWithLogo(t *testing.T) {
	out := render(t, "https://app.example.com/", Content{Heading: "Hello"})
	if !strings.Contains(out, `bgcolor="#101828"`) {
		t.Errorf("brand bar is not navy:\n%s", out)
	}
	if !strings.Contains(out, `<img src="https://app.example.com/email-logo.png"`) {
		t.Errorf("logo should load from the app origin:\n%s", out)
	}
}

// Without an origin there is nowhere to load the logo from: the bar shows the
// wordmark alone instead of a broken image.
func TestRender_NoBaseURLOmitsLogo(t *testing.T) {
	out := render(t, "", Content{Heading: "Hello"})
	if strings.Contains(out, "<img") {
		t.Errorf("logo rendered without a base URL:\n%s", out)
	}
	if !strings.Contains(out, HeaderMarker) {
		t.Errorf("wordmark missing:\n%s", out)
	}
}

func TestAppURL(t *testing.T) {
	cases := map[string]struct{ base, want string }{
		"origin":         {"https://app.example.com", "https://app.example.com/settings"},
		"trailing slash": {"https://app.example.com/", "https://app.example.com/settings"},
		"no origin":      {"", ""},
		"slash only":     {"/", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := AppURL(tc.base, "/settings"); got != tc.want {
				t.Errorf("AppURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// Mail clients rarely load web fonts, and a serif stack falls back to Georgia,
// so every text style is the sans stack.
func TestRender_UsesSansFontOnly(t *testing.T) {
	out := render(t, "https://app.example.com", Content{
		Heading: "Hello", Body: Join(P("x"), Callout("Reason", "y")),
		Details: []Row{{Label: "a", Value: "b"}}, Button: &Button{Label: "Go", URL: "https://example.com"},
		Notes: Join(Note("n"), LinkFallback("https://example.com")),
	})
	for _, serif := range []string{"Georgia", "Source Serif", "Times New Roman"} {
		if strings.Contains(out, serif) {
			t.Errorf("output uses serif font %q:\n%s", serif, out)
		}
	}
	if got := strings.Count(out, "font-family: "+fontStack); got < 7 {
		t.Errorf("sans stack on %d elements, want every text element:\n%s", got, out)
	}
}

func TestRender_OptionalPartsOmitted(t *testing.T) {
	out := render(t, "https://app.example.com", Content{Heading: "Hello", Body: P("x")})
	for _, unwanted := range []string{`<a href`, `display: none`, `border-top: 1px solid #f2f4f7`, `bgcolor="#155dfc"`} {
		if strings.Contains(out, unwanted) {
			t.Errorf("empty content should not render %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, string(defaultFooter)) {
		t.Errorf("missing default footer:\n%s", out)
	}
}

// Rows after the first are separated by a line; the first has none.
func TestRender_DetailRowSeparators(t *testing.T) {
	out := render(t, "", Content{Details: []Row{{Label: "A", Value: "1"}, {Label: "B", Value: "2"}, {Label: "C", Value: "3"}}})
	if got := strings.Count(out, "border-top: 1px solid #f2f4f7;"); got != 4 {
		t.Errorf("want 2 separated rows (4 cells), got %d cells:\n%s", got, out)
	}
}

func TestRender_ButtonRejectsUnsafeURL(t *testing.T) {
	email, err := Render("", Content{Button: &Button{Label: "Go", URL: "javascript:alert(1)"}})
	if err != nil {
		t.Fatal(err)
	}
	out := email.HTML()
	if strings.Contains(out, "javascript:") {
		t.Errorf("button kept a javascript: URL:\n%s", out)
	}
}

func TestHelpers_EscapeUserText(t *testing.T) {
	const evil = `Acme <script>alert(1)</script> & "co"`
	email, err := Render("https://app.example.com", Content{
		Preheader: evil,
		Heading:   evil,
		Body: Join(
			P("Org %s", evil),
			Callout(evil, evil),
			P("%s", Strong(evil)),
			P("%s", Link(`https://example.com/?a="x"&b=<y>`, evil)),
		),
		Details: []Row{{Label: evil, Value: evil}},
		Button:  &Button{Label: evil, URL: `https://example.com/?a="x"&b=<y>`},
		Notes:   Join(Note("By %s", evil), LinkFallback(`https://example.com/?a="x"&b=<y>`)),
		Footer:  Text("Footer %s", evil),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := email.HTML()
	if strings.Contains(out, "<script>") {
		t.Errorf("unescaped script tag in output:\n%s", out)
	}
	if strings.Contains(out, `"x"&b=<y>`) {
		t.Errorf("unescaped URL in output:\n%s", out)
	}
	if got := strings.Count(out, "&lt;script&gt;"); got < 10 {
		t.Errorf("want every field escaped, found %d escaped script tags:\n%s", got, out)
	}
}

func TestP_TrustsHTMLArgs(t *testing.T) {
	got := string(P("See %s", Link("https://example.com", "here")))
	if !strings.Contains(got, `<a href="https://example.com"`) {
		t.Errorf("Link inside P should stay markup, got %q", got)
	}
}

func TestCallout_KeepsLineBreaks(t *testing.T) {
	got := string(Callout("Comment:", "line one\nline two"))
	if !strings.Contains(got, "white-space: pre-line") || !strings.Contains(got, "line one\nline two") {
		t.Errorf("a multi-line note should keep its line breaks, got %q", got)
	}
}
