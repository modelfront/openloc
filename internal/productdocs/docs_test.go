package productdocs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

// fixture is a tiny valid documentation set: two sections, three pages with
// different role sets, one asset.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"content/guides/_index.md": {Data: []byte("---\ntitle: Guides\norder: 20\nsummary: How-to.\n---\nIntro text.\n")},
		"content/guides/cat-editor.md": {Data: []byte(`---
title: The CAT editor
summary: Translate segments.
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-09-08
sources: [web/src/pages/CATEditor.tsx]
related: [reference/roles, guides/vendors]
screenshots: [cat-editor.png]
---
# Editing

Press **Ctrl+Enter** to confirm a segment.

## Tags

Tags are shown as chips.
`)},
		"content/guides/vendors.md": {Data: []byte(`---
title: Vendors
summary: Invite external linguists.
order: 10
roles: [owner, admin]
updated_at: 2026-09-01
---
Invite a vendor from the Vendors page. Vendors work in the editor like everyone else.
`)},
		"content/reference/_index.md": {Data: []byte("---\ntitle: Reference\norder: 30\n---\n")},
		"content/reference/roles.md": {Data: []byte(`---
title: Roles
summary: Who can do what.
order: 10
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-09-02
---
`)},
		"content/assets/cat-editor.png": {Data: []byte("\x89PNG fake")},
	}
}

func mustIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := New(fixture(), "content")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return idx
}

func TestNew_ParsesSectionsPagesAndAssets(t *testing.T) {
	idx := mustIndex(t)
	if got := len(idx.Sections()); got != 2 {
		t.Fatalf("sections: want 2, got %d", got)
	}
	// Sorted by order: guides (20) before reference (30).
	if idx.Sections()[0].Slug != "guides" || idx.Sections()[1].Slug != "reference" {
		t.Fatalf("section order wrong: %s, %s", idx.Sections()[0].Slug, idx.Sections()[1].Slug)
	}
	if idx.Sections()[0].Body != "Intro text." {
		t.Fatalf("section body: %q", idx.Sections()[0].Body)
	}
	p := idx.Pages()["guides/cat-editor"]
	if p == nil {
		t.Fatal("slug guides/cat-editor missing — slug must derive from the path")
	}
	if p.Section != "guides" || p.Title != "The CAT editor" || p.UpdatedAt != "2026-09-08" {
		t.Fatalf("page fields: %+v", p)
	}
	if strings.HasPrefix(p.Body, "---") || !strings.HasPrefix(p.Body, "# Editing") {
		t.Fatalf("body should start after frontmatter, got %q", p.Body[:20])
	}
	if p.Stub {
		t.Fatal("page with a body must not be a stub")
	}
	if !idx.Pages()["reference/roles"].Stub {
		t.Fatal("page with empty body must be a stub")
	}
	// Pages within a section sort by order: vendors (10) before cat-editor (20).
	if idx.Sections()[0].Pages[0].Slug != "guides/vendors" {
		t.Fatalf("page order wrong: %s", idx.Sections()[0].Pages[0].Slug)
	}
	if _, ok := idx.Asset("cat-editor.png"); !ok {
		t.Fatal("asset not indexed")
	}
}

func TestNew_RejectsMalformedContent(t *testing.T) {
	cases := map[string]func(fstest.MapFS){
		"missing frontmatter": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("# no frontmatter\n")}
		},
		"unclosed frontmatter": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\n")}
		},
		"missing title": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\n---\n")}
		},
		"missing summary": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nroles: [owner]\nupdated_at: 2026-01-01\n---\n")}
		},
		"no roles": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nupdated_at: 2026-01-01\n---\n")}
		},
		"unknown role": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [member]\nupdated_at: 2026-01-01\n---\n")}
		},
		"bad date": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: yesterday\n---\n")}
		},
		"typo in key": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdate_at: 2026-01-01\n---\n")}
		},
		"dangling related": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\nrelated: [guides/nope]\n---\n")}
		},
		"screenshot with a bad name": func(m fstest.MapFS) {
			m["content/guides/bad.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\nscreenshots: [../etc/passwd]\n---\n")}
		},
		"section without _index": func(m fstest.MapFS) {
			m["content/misc/page.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\n---\n")}
		},
		"non-kebab file name": func(m fstest.MapFS) {
			m["content/guides/Bad_Name.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\n---\n")}
		},
		"nested directory": func(m fstest.MapFS) {
			m["content/guides/deep/page.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\n---\n")}
		},
		"stray non-markdown file": func(m fstest.MapFS) {
			m["content/guides/notes.txt"] = &fstest.MapFile{Data: []byte("x")}
		},
		"asset with bad name": func(m fstest.MapFS) {
			m["content/assets/Shot 1.PNG"] = &fstest.MapFile{Data: []byte("x")}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := fixture()
			mutate(m)
			if _, err := New(m, "content"); err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

// A screenshot that has not been shot yet must not stop the server — the
// server is what `make docs-screenshots` drives to take it. It is reported
// instead, and TestContent_EmbeddedContentParses holds the shipped content to
// zero missing.
func TestNew_MissingScreenshotIsReportedNotFatal(t *testing.T) {
	m := fixture()
	m["content/guides/pending.md"] = &fstest.MapFile{Data: []byte("---\ntitle: X\nsummary: s\nroles: [owner]\nupdated_at: 2026-01-01\nscreenshots: [not-yet.png, cat-editor.png]\n---\n")}
	idx, err := New(m, "content")
	if err != nil {
		t.Fatalf("missing screenshot must not be fatal: %v", err)
	}
	if got := idx.MissingScreenshots(); len(got) != 1 || got[0] != "guides/pending: not-yet.png" {
		t.Fatalf("missing: %v", got)
	}
	if got := mustIndex(t).MissingScreenshots(); len(got) != 0 {
		t.Fatalf("complete fixture reports missing screenshots: %v", got)
	}
}

func TestTreeFor_FiltersByRole(t *testing.T) {
	idx := mustIndex(t)

	admin := idx.TreeFor("admin")
	if len(admin) != 2 || len(admin[0].Pages) != 2 {
		t.Fatalf("admin should see both guides pages: %+v", admin)
	}

	linguist := idx.TreeFor("linguist")
	if len(linguist) != 2 {
		t.Fatalf("linguist sections: want 2, got %d", len(linguist))
	}
	if len(linguist[0].Pages) != 1 || linguist[0].Pages[0].Slug != "guides/cat-editor" {
		t.Fatalf("linguist must see only the editor guide, got %+v", linguist[0].Pages)
	}

	// A role that may read nothing gets an empty (non-nil) tree.
	if got := idx.TreeFor("nobody"); got == nil || len(got) != 0 {
		t.Fatalf("unknown role: want [], got %v", got)
	}
}

func TestPageFor_HidesPagesTheRoleMayNotRead(t *testing.T) {
	idx := mustIndex(t)

	if _, ok := idx.PageFor("guides/vendors", "admin"); !ok {
		t.Fatal("admin should read vendors")
	}
	if _, ok := idx.PageFor("guides/vendors", "linguist"); ok {
		t.Fatal("linguist must not read the admin-only vendors page")
	}
	if _, ok := idx.PageFor("guides/nope", "admin"); ok {
		t.Fatal("unknown slug must miss")
	}

	page, _ := idx.PageFor("guides/cat-editor", "linguist")
	if page.SectionTitle != "Guides" {
		t.Fatalf("section title: %q", page.SectionTitle)
	}

	// The editor guide links the owner/admin vendors page: only roles that may
	// read it see it among the related pages.
	for role, want := range map[string]string{
		"admin":       "reference/roles,guides/vendors",
		"linguist":    "reference/roles",
		RoleAnonymous: "reference/roles",
	} {
		page, ok := idx.PageFor("guides/cat-editor", role)
		if !ok {
			t.Fatalf("role %q should read the editor guide", role)
		}
		var got []string
		for _, rel := range page.Related {
			got = append(got, rel.Slug)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("role %q related: want %s, got %v", role, want, got)
		}
	}
}

func TestSearch_RanksAndFiltersByRole(t *testing.T) {
	idx := mustIndex(t)

	hits := idx.Search("vendor", "admin")
	if len(hits) != 1 || hits[0].Slug != "guides/vendors" {
		t.Fatalf("admin vendor search: %+v", hits)
	}
	if hits := idx.Search("vendor", "linguist"); len(hits) != 0 {
		t.Fatalf("linguist must not find the admin-only page: %+v", hits)
	}

	// Title hits outrank body hits: "editor" is in the cat-editor title and
	// only in the vendors body, so both match and cat-editor must sort first.
	hits = idx.Search("editor", "owner")
	if len(hits) != 2 || hits[0].Slug != "guides/cat-editor" || hits[1].Slug != "guides/vendors" {
		t.Fatalf("title must outrank body: %+v", hits)
	}
	// A title-only hit falls back to the summary as its snippet.
	if hits[0].Snippet != "Translate segments." {
		t.Fatalf("title-only hit should show the summary, got %q", hits[0].Snippet)
	}
	// A body hit shows plain text around the term: the source line is
	// "Press **Ctrl+Enter** to confirm a segment." and the bold markers go.
	hits = idx.Search("confirm", "owner")
	if len(hits) != 1 || hits[0].Slug != "guides/cat-editor" {
		t.Fatalf("confirm search: %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "Ctrl+Enter to confirm") || strings.ContainsAny(hits[0].Snippet, "#*") {
		t.Fatalf("snippet must be the body text around the hit with markup stripped, got %q", hits[0].Snippet)
	}

	// Every term must match: "tags" is in cat-editor, "vendor" is not.
	if hits := idx.Search("tags vendor", "owner"); len(hits) != 0 {
		t.Fatalf("AND semantics: %+v", hits)
	}
	// Stubs never show up in search.
	if hits := idx.Search("roles", "owner"); len(hits) != 0 {
		t.Fatalf("stub page must not be searchable: %+v", hits)
	}
	if hits := idx.Search("   ", "owner"); len(hits) != 0 {
		t.Fatalf("blank query: %+v", hits)
	}
}

var anchorHeadingRe = regexp.MustCompile(`^(#{2,3})\s+(.+?)\s*#*\s*$`)

// anchorIDs lists the heading ids a browser will find on a rendered page.
//
// This duplicates web/src/lib/markdown-headings.ts (slugifyHeading +
// extractHeadings) because the ids are assigned in TypeScript at render time
// and Go cannot call it. Package-level extractHeadings is not a substitute: it
// lowercases every level for search and does not slugify. The two must stay in
// step, and this test is what holds them together — change one, change both.
//
// Only ## and ### get ids; fenced blocks are skipped; a repeated heading text
// gets a -2, -3 suffix.
func anchorIDs(body string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, " \t")
		if s := strings.TrimLeft(line, " \t"); strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := anchorHeadingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		base := slugifyHeading(m[2])
		if base == "" {
			continue
		}
		seen[base]++
		if n := seen[base]; n > 1 {
			out[fmt.Sprintf("%s-%d", base, n)] = true
			continue
		}
		out[base] = true
	}
	return out
}

// slugifyHeading is the Go port of slugifyHeading in markdown-headings.ts:
// lowercase, drop emphasis and link punctuation, drop anything that is not a
// letter, number, space or hyphen, then collapse runs of space and hyphen.
func slugifyHeading(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if strings.ContainsRune("`*_~[]()", r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.FieldsFunc(b.String(), unicode.IsSpace), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

func sortedKeys(m map[string]*Page) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestAnonymous_SeesOnlyPagesEveryRoleReads(t *testing.T) {
	idx := mustIndex(t)

	tree := idx.TreeFor(RoleAnonymous)
	var slugs []string
	for _, s := range tree {
		for _, p := range s.Pages {
			slugs = append(slugs, p.Slug)
		}
	}
	sort.Strings(slugs)
	if want := "guides/cat-editor,reference/roles"; strings.Join(slugs, ",") != want {
		t.Fatalf("anonymous tree: want %s, got %v", want, slugs)
	}
	if _, ok := idx.PageFor("guides/vendors", RoleAnonymous); ok {
		t.Fatal("anonymous must not read the owner/admin vendors page")
	}
	if _, ok := idx.PageFor("guides/cat-editor", RoleAnonymous); !ok {
		t.Fatal("anonymous should read the all-role editor guide")
	}
	for _, hit := range idx.Search("vendor", RoleAnonymous) {
		if hit.Slug == "guides/vendors" {
			t.Fatal("anonymous search leaks the vendors page")
		}
	}
}
