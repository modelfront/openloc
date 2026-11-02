package docseo

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"openloc/internal/productdocs"
	"openloc/internal/publicapi"
)

const everyone = "roles: [owner, admin, project_manager, linguist]\nupdated_at: 2026-09-08\n"

func helpIndex(t *testing.T) *productdocs.Index {
	t.Helper()
	idx, err := productdocs.New(fstest.MapFS{
		"content/guides/_index.md": {Data: []byte("---\ntitle: Guides\norder: 20\nsummary: How-to.\n---\nSection intro.\n")},
		"content/guides/cat-editor.md": {Data: []byte("---\ntitle: The CAT editor\nsummary: Translate <segments>.\norder: 10\n" + everyone +
			"related: [guides/vendors, guides/stub]\n---\n## Confirm\n\nSee [roles](/docs/guides/vendors#x), [keys](/api-docs/getting-started/keys), " +
			"[projects](/projects) and [web](https://example.com).\n\n![Editor](assets/cat-editor.png)\n")},
		"content/guides/vendors.md": {Data: []byte("---\ntitle: Vendors\nsummary: Invite linguists.\norder: 20\nroles: [owner, admin]\nupdated_at: 2026-09-01\n---\nOwners only.\n")},
		"content/guides/stub.md":    {Data: []byte("---\ntitle: Soon\nsummary: Not written.\norder: 30\n" + everyone + "---\n")},
		"content/admin/_index.md":   {Data: []byte("---\ntitle: Admin\norder: 30\nsummary: Admin only.\n---\n")},
		"content/admin/billing.md":  {Data: []byte("---\ntitle: Billing\nsummary: Pay.\norder: 10\nroles: [owner]\nupdated_at: 2026-09-01\n---\nPay.\n")},
	}, "content")
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func apiIndex(t *testing.T) *productdocs.Index {
	t.Helper()
	idx, err := productdocs.API()
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func spec(t *testing.T) *publicapi.Spec {
	t.Helper()
	s, err := publicapi.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testSites(t *testing.T) (*Sites, *Site, *Site) {
	help := &Site{Host: "docs.example.com", Origin: "https://docs.example.com", Prefix: "/docs", API: "/api/docs", Label: "Docs",
		Home: Home{Title: "Docs", Description: "How it works."}, Docs: helpIndex(t)}
	dev := &Site{Host: "Developer.example.com", Origin: "https://developer.example.com", Prefix: "/api-docs", API: "/api/api-docs", Label: "API",
		Home: Home{Title: "API", Description: "Connect."}, Docs: apiIndex(t), Spec: spec(t)}
	return New("https://app.example.com/", help, dev), help, dev
}

func TestForHost(t *testing.T) {
	sites, help, dev := testSites(t)
	if sites.ForHost("docs.example.com") != help || sites.ForHost("DEVELOPER.example.com") != dev {
		t.Error("docs hosts not matched")
	}
	if sites.ForHost("app.example.com") != nil || sites.ForHost("") != nil {
		t.Error("a non-docs host matched")
	}
	off := New("https://app.example.com", &Site{Prefix: "/docs", Docs: helpIndex(t)})
	if off.ForHost("") != nil {
		t.Error("a site without a host matched")
	}
	var none *Sites
	if none.ForHost("docs.example.com") != nil {
		t.Error("nil set matched")
	}
}

func TestPage_HelpArticle(t *testing.T) {
	_, help, _ := testSites(t)
	p, ok := help.Page("/guides/cat-editor/")
	if !ok {
		t.Fatal("public page not rendered")
	}
	if p.Title != "The CAT editor · Docs · OpenLoc" || p.Description != "Translate <segments>." ||
		p.Canonical != "https://docs.example.com/guides/cat-editor" {
		t.Errorf("meta = %+v", p)
	}
	body := string(p.Body)
	for _, want := range []string{
		"<h1>The CAT editor</h1>",
		"Translate &lt;segments&gt;.",
		`<h2 id="confirm">Confirm</h2>`,
		`href="/guides/vendors#x"`,
		`href="https://developer.example.com/getting-started/keys"`,
		`href="https://app.example.com/projects"`,
		`href="https://example.com"`,
		`src="/api/docs/assets/cat-editor.png"`,
		`<a href="/guides/stub">Soon</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	// A related page anonymous readers may not see is not listed.
	if strings.Contains(body, "Invite linguists") {
		t.Errorf("owner-only related page listed:\n%s", body)
	}
}

func TestPage_OnlyAnonymousPages(t *testing.T) {
	_, help, dev := testSites(t)
	for _, path := range []string{"/guides/vendors", "/admin", "/admin/billing", "/guides/nope", "/nope", "/a/b/c", "/endpoints"} {
		if _, ok := help.Page(path); ok {
			t.Errorf("%s rendered", path)
		}
	}
	for _, path := range []string{"/endpoints/nope", "/endpoints/a/b"} {
		if _, ok := dev.Page(path); ok {
			t.Errorf("developer %s rendered", path)
		}
	}
}

func TestPage_HomeAndSection(t *testing.T) {
	_, help, _ := testSites(t)
	home, ok := help.Page("/")
	if !ok || home.Title != "Docs · OpenLoc" || home.Canonical != "https://docs.example.com/" || home.Description != "How it works." {
		t.Fatalf("home = %+v, %v", home, ok)
	}
	if b := string(home.Body); !strings.Contains(b, `<a href="/guides">Guides</a>`) || !strings.Contains(b, `href="/guides/cat-editor"`) ||
		strings.Contains(b, "Admin") || strings.Contains(b, "Vendors") || strings.Contains(b, "/endpoints") {
		t.Errorf("home body:\n%s", b)
	}
	sec, ok := help.Page("/guides")
	if !ok || sec.Title != "Guides · Docs · OpenLoc" || sec.Canonical != "https://docs.example.com/guides" {
		t.Fatalf("section = %+v, %v", sec, ok)
	}
	if b := string(sec.Body); !strings.Contains(b, "Section intro.") || !strings.Contains(b, `href="/guides/cat-editor"`) {
		t.Errorf("section body:\n%s", b)
	}
}

func TestPage_Endpoints(t *testing.T) {
	_, _, dev := testSites(t)
	var visible string
	for _, path := range dev.Spec.Doc.Paths.InMatchingOrder() {
		for _, op := range dev.Spec.Doc.Paths.Value(path).Operations() {
			if !publicapi.XHidden(op) && visible == "" {
				visible = op.OperationID
			}
		}
	}
	if visible == "" {
		t.Fatal("spec has no visible operation")
	}
	p, ok := dev.Page("/endpoints/" + visible)
	if !ok {
		t.Fatalf("endpoint %s not rendered", visible)
	}
	if p.Canonical != "https://developer.example.com/endpoints/"+visible || !strings.HasSuffix(p.Title, " · API · OpenLoc") ||
		!strings.Contains(string(p.Body), "<code>") || !strings.Contains(p.Description, "/api/v1/") {
		t.Errorf("endpoint page = %+v", p)
	}
	// getOpenAPISpec is marked x-hidden in base.yaml: no page, no link, no sitemap entry.
	const hidden = "getOpenAPISpec"
	if _, ok := dev.Page("/endpoints/" + hidden); ok {
		t.Errorf("hidden endpoint %s rendered", hidden)
	}
	sitemap, err := dev.Sitemap()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sitemap), "/endpoints/"+hidden+"<") {
		t.Errorf("sitemap lists hidden endpoint %s", hidden)
	}
	all, ok := dev.Page("/endpoints")
	if !ok || all.Title != "API documentation · OpenLoc" || !strings.Contains(string(all.Body), `href="/endpoints/`+visible+`"`) {
		t.Errorf("overview = %+v", all)
	}
	if strings.Contains(string(all.Body), `href="/endpoints/`+hidden+`"`) {
		t.Errorf("overview links hidden endpoint %s", hidden)
	}
	home, _ := dev.Page("/")
	if !strings.Contains(string(home.Body), `<a href="/endpoints">API documentation</a>`) {
		t.Errorf("developer home lacks the endpoint reference:\n%s", home.Body)
	}
}

func TestPage_EveryRealPageRenders(t *testing.T) {
	help := &Site{Host: "docs.example.com", Origin: "https://docs.example.com", Prefix: "/docs", API: "/api/docs", Label: "Docs"}
	idx, err := productdocs.Default()
	if err != nil {
		t.Fatal(err)
	}
	help.Docs = idx
	_, _, dev := testSites(t)
	New("https://app.example.com", help, dev)
	for _, site := range []*Site{help, dev} {
		raw, err := site.Sitemap()
		if err != nil {
			t.Fatal(err)
		}
		var set urlset
		if err := xml.Unmarshal(raw, &set); err != nil {
			t.Fatal(err)
		}
		for _, u := range set.URLs {
			if _, ok := site.Page(strings.TrimPrefix(u.Loc, site.Origin)); !ok {
				t.Errorf("sitemap lists %s, which does not render", u.Loc)
			}
		}
	}
}

func TestSitemap(t *testing.T) {
	_, help, dev := testSites(t)
	raw, err := help.Sitemap()
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		`<?xml`, `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`,
		"<loc>https://docs.example.com/</loc>",
		"<loc>https://docs.example.com/guides</loc>",
		"<loc>https://docs.example.com/guides/cat-editor</loc>\n    <lastmod>2026-09-08</lastmod>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sitemap lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"vendors", "admin", "billing", "/guides/stub", "endpoints"} {
		if strings.Contains(got, not) {
			t.Errorf("sitemap lists %q:\n%s", not, got)
		}
	}
	raw, err = dev.Sitemap()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "<loc>https://developer.example.com/endpoints/") {
		t.Errorf("developer sitemap lacks endpoints:\n%s", raw)
	}
}

func TestInject(t *testing.T) {
	index := []byte("<html><head>\n<title>OpenLoc</title>\n</head><body>\n<div id=\"root\"></div>\n<script src=\"/a.js\"></script></body></html>")
	out, err := Inject(index, Page{Title: `A "b" <c>`, Description: "d & e", Canonical: "https://docs.example.com/x", Body: "<p>hi</p>"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"<title>A &#34;b&#34; &lt;c&gt;</title>",
		`<meta name="description" content="d &amp; e" />`,
		`<link rel="canonical" href="https://docs.example.com/x" />`,
		`<meta property="og:title" content="A &#34;b&#34; &lt;c&gt;" />`,
		`<div id="root"><div data-prerendered><p>hi</p></div></div>`,
		`<script src="/a.js"></script>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "<title>") != 1 {
		t.Errorf("title count:\n%s", got)
	}
	if _, err := Inject([]byte("<html><body>no markers</body></html>"), Page{Title: "x"}); err == nil {
		t.Error("index without markers: no error")
	}
}

// The app's real index.html has the markers Inject fills.
func TestInject_AppIndex(t *testing.T) {
	index, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Inject(index, Page{Title: "T", Body: "<p>page text</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<div id="root"><div data-prerendered><p>page text</p></div></div>`) || !strings.Contains(string(out), "<title>T</title>") {
		t.Errorf("web/index.html not filled:\n%s", out)
	}
}

// With only the help on its own host, links to the API docs stay on the app.
func TestPage_OtherSiteWithoutHost(t *testing.T) {
	help := &Site{Host: "docs.example.com", Origin: "https://docs.example.com", Prefix: "/docs", API: "/api/docs", Label: "Docs", Docs: helpIndex(t)}
	dev := &Site{Prefix: "/api-docs", API: "/api/api-docs", Label: "API", Docs: apiIndex(t)}
	New("https://app.example.com", help, dev)
	p, ok := help.Page("/guides/cat-editor")
	if !ok {
		t.Fatal("page not rendered")
	}
	if want := `href="https://app.example.com/api-docs/getting-started/keys"`; !strings.Contains(string(p.Body), want) {
		t.Errorf("body lacks %s:\n%s", want, p.Body)
	}
}
