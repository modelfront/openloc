package productdocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docSet is one embedded documentation set and the SPA route its pages link
// under.
type docSet struct {
	name      string
	load      func() (*Index, error)
	routeBase string // "/docs/" — body links to other pages start with it
}

var docSets = []docSet{
	{name: "user", load: Default, routeBase: "/docs/"},
	{name: "api", load: API, routeBase: "/api-docs/"},
}

// forEachSet runs fn once per documentation set, with the set's index loaded.
func forEachSet(t *testing.T, fn func(t *testing.T, set docSet, idx *Index)) {
	for _, set := range docSets {
		t.Run(set.name, func(t *testing.T) {
			idx, err := set.load()
			if err != nil {
				t.Fatalf("embedded content: %v", err)
			}
			fn(t, set, idx)
		})
	}
}

// The embedded content is what the server boots with; if it fails to parse,
// the server fails to start. Cheap to prove here.
func TestContent_EmbeddedContentParses(t *testing.T) {
	forEachSet(t, func(t *testing.T, _ docSet, idx *Index) {
		if len(idx.Sections()) == 0 {
			t.Fatal("embedded content has no sections")
		}
		// Strict here, lenient at runtime: a page may reference a screenshot
		// before it is shot so the server can start and shoot it, but nothing
		// ships that way.
		if m := idx.MissingScreenshots(); len(m) > 0 {
			t.Fatalf("screenshots referenced but not in assets/ — run `make docs-screenshots`:\n  %s", strings.Join(m, "\n  "))
		}
		// Every role must see at least one page — the docs are for everyone.
		for _, role := range AllRoles {
			if len(idx.TreeFor(role)) == 0 {
				t.Fatalf("role %s sees no documentation", role)
			}
		}
	})
}

// A `related:` slug that does not resolve is a startup error (see New), but a
// link in a page body is only followed by a reader. This pins that every body
// link to another page of the same set, or to a #fragment, resolves to a page
// and a heading.
func TestContent_InternalLinksResolve(t *testing.T) {
	forEachSet(t, func(t *testing.T, set docSet, idx *Index) {
		linkRe := regexp.MustCompile(`\]\((` + regexp.QuoteMeta(set.routeBase) + `[^)\s]*|#[^)\s]*)\)`)

		anchors := make(map[string]map[string]bool, len(idx.Pages()))
		for slug, p := range idx.Pages() {
			anchors[slug] = anchorIDs(p.Body)
		}
		sections := map[string]bool{}
		for _, s := range idx.Sections() {
			sections[s.Slug] = true
		}

		for _, slug := range sortedKeys(idx.Pages()) {
			for _, m := range linkRe.FindAllStringSubmatch(idx.Pages()[slug].Body, -1) {
				target := m[1]
				targetSlug, frag := slug, ""
				if rest, ok := strings.CutPrefix(target, set.routeBase); ok {
					targetSlug, frag = rest, ""
					if i := strings.IndexByte(rest, '#'); i >= 0 {
						targetSlug, frag = rest[:i], rest[i+1:]
					}
				} else {
					frag = strings.TrimPrefix(target, "#")
				}

				// A link to a section index has no headings of its own.
				if sections[targetSlug] {
					continue
				}
				ids, ok := anchors[targetSlug]
				if !ok {
					t.Errorf("%s: link %q points at a page that does not exist", slug, target)
					continue
				}
				if frag != "" && !ids[frag] {
					t.Errorf("%s: link %q has no matching heading on %s\n  headings there: %s",
						slug, target, targetSlug, strings.Join(sortedSet(ids), ", "))
				}
			}
		}
	})
}

// inlineImageRe matches a Markdown image and captures its source.
var inlineImageRe = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`)

// assetPrefixRe mirrors the renderer, which resolves an image source against
// the asset endpoint after stripping a leading ./ and assets/ (see
// web/src/components/markdown/Markdown.tsx).
var assetPrefixRe = regexp.MustCompile(`^(\./)?(assets/)?`)

// MissingScreenshots only covers the `screenshots:` frontmatter. Diagrams are
// embedded inline without being listed there — they are drawn, not shot, so
// they must stay out of the screenshot pipeline — which would leave a typo in
// an inline source to 404 silently in the browser. This covers every inline
// image, diagrams and screenshots alike.
func TestContent_InlineImagesResolve(t *testing.T) {
	forEachSet(t, func(t *testing.T, _ docSet, idx *Index) {
		for _, slug := range sortedKeys(idx.Pages()) {
			for _, m := range inlineImageRe.FindAllStringSubmatch(idx.Pages()[slug].Body, -1) {
				src := m[1]
				if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
					continue
				}
				name := assetPrefixRe.ReplaceAllString(src, "")
				if _, ok := idx.Asset(name); !ok {
					t.Errorf("%s: image %q has no file in assets/", slug, src)
				}
			}
		}
	})
}

// repoRoot is the repository root, relative to this package's directory, which
// is where `go test` runs.
const repoRoot = "../.."

// `sources:` is how /docs-sync routes a code change to the pages it can make
// wrong. An entry that matches nothing — a renamed or deleted file, a moved
// package — silently stops routing changes to its pages, so every entry must
// match at least one file. The content ships in the public repo, so this also
// keeps pages from citing a file that only exists in the private one.
func TestContent_SourcesResolve(t *testing.T) {
	forEachSet(t, func(t *testing.T, _ docSet, idx *Index) {
		for _, slug := range sortedKeys(idx.Pages()) {
			for _, src := range idx.Pages()[slug].Sources {
				if dir, ok := strings.CutSuffix(src, "/**"); ok {
					if !dirHasFile(filepath.Join(repoRoot, dir)) {
						t.Errorf("%s: source %q matches no file", slug, src)
					}
					continue
				}
				if strings.Contains(src, "**") {
					t.Errorf("%s: source %q: `**` is supported only as a trailing /**", slug, src)
					continue
				}
				if strings.ContainsAny(src, "*?[") {
					if m, _ := filepath.Glob(filepath.Join(repoRoot, src)); len(m) == 0 {
						t.Errorf("%s: source %q matches no file", slug, src)
					}
					continue
				}
				if fi, err := os.Stat(filepath.Join(repoRoot, src)); err != nil || fi.IsDir() {
					t.Errorf("%s: source %q is not a file in the repo", slug, src)
				}
			}
		}
	})
}

// dirHasFile reports whether dir exists and holds at least one regular file at
// any depth.
func dirHasFile(dir string) bool {
	found := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return err == nil && found
}

// The known-limitations register belongs to the user documentation. An API
// guide that named a register id would be checked by nothing, so none may.
func TestAPI_NoLimitations(t *testing.T) {
	idx, err := API()
	if err != nil {
		t.Fatalf("embedded content: %v", err)
	}
	for _, slug := range sortedKeys(idx.Pages()) {
		if l := idx.Pages()[slug].Limitations; len(l) > 0 {
			t.Errorf("%s: API guides carry no limitations: (found %v)", slug, l)
		}
	}
}

// /api-docs/endpoints/... is the endpoint reference, rendered from the
// OpenAPI spec beside these guides, so no guide section may take that slug.
func TestAPI_EndpointsSlugIsFree(t *testing.T) {
	idx, err := API()
	if err != nil {
		t.Fatalf("embedded content: %v", err)
	}
	for _, s := range idx.Sections() {
		if s.Slug == "endpoints" {
			t.Fatal("an API guide section is named endpoints, which the endpoint reference owns")
		}
	}
}
