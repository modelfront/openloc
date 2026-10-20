// Package productdocs serves the in-app documentation sets: Markdown pages
// embedded in the binary, indexed once at startup, filtered per role. There are
// two sets: the user documentation (content/, /api/docs, the /docs page) and
// the API guides (apicontent/, /api/api-docs, the /api-docs page).
//
// Content lives in <set>/<section>/<page>.md. A page's slug is its path
// (guides/cat-editor) — never stored in the file, so it cannot disagree with
// reality. Each section directory has an _index.md carrying the section's
// title, order and intro. Screenshots live flat in content/assets/.
//
// Docs are global product content, not tenant data, so nothing here is scoped
// by tenant_id — auth and role are the only gates. This is the one place the
// tenant-scoping rule deliberately does not apply.
//
// The parser takes any fs.FS, so each set is a New() call on its own embedded
// FS.
package productdocs

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"openloc/internal/auth"
)

//go:embed all:content
var contentFS embed.FS

// Default builds the index over the embedded user-documentation content.
func Default() (*Index, error) { return New(contentFS, "content") }

//go:embed all:apicontent
var apiContentFS embed.FS

// API builds the index over the embedded API guides. The endpoint reference
// itself is the OpenAPI spec in internal/publicapi, not pages here.
func API() (*Index, error) { return New(apiContentFS, "apicontent") }

// ValidRoles is the closed set a page's `roles:` may name. Kept in sync with
// internal/auth/roles.go by construction (the constants are reused).
var ValidRoles = map[string]bool{
	auth.RoleOwner:          true,
	auth.RoleAdmin:          true,
	auth.RoleProjectManager: true,
	auth.RoleLinguist:       true,
}

// AllRoles lists every role, in the order the docs treat as canonical.
var AllRoles = []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleProjectManager, auth.RoleLinguist}

// slugRe is the shape of a section slug and of a page's file stem.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// assetRe restricts assets to a flat directory of image files.
var assetRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*\.(png|jpg|jpeg|gif|svg|webp)$`)

// Page is one documentation page with its frontmatter and body.
type Page struct {
	Slug        string // section/stem, e.g. guides/cat-editor
	Section     string // section slug
	Title       string
	Summary     string
	Order       int
	Roles       []string // who may read it; subset of ValidRoles
	UpdatedAt   string   // YYYY-MM-DD
	Sources     []string // code paths this page describes (docs-sync input)
	Limitations []string // known-limitations register ids this page describes
	Related     []string // slugs of related pages
	Screenshots []string // asset file names the page embeds
	Body        string   // markdown without the frontmatter block
	Stub        bool     // true when Body is empty — the page is not written yet

	headings []string // lowercased, for search
}

// Section groups the pages of one content directory.
type Section struct {
	Slug    string
	Title   string
	Summary string
	Order   int
	Body    string
	Pages   []*Page // sorted by Order, then Title
}

// Asset is one embedded image.
type Asset struct {
	Name string
	Data []byte
	ETag string
}

// Index is the parsed, validated documentation set. Immutable after New.
type Index struct {
	sections []*Section
	pages    map[string]*Page
	assets   map[string]*Asset
	missing  []string // "slug: name.png" for every referenced screenshot with no asset
}

// New parses every page under root in fsys. Any malformed page is an error —
// the server refuses to start rather than 500 a user later.
func New(fsys fs.FS, root string) (*Index, error) {
	idx := &Index{pages: map[string]*Page{}, assets: map[string]*Asset{}}

	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("productdocs: read %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == "assets" {
			if err := idx.loadAssets(fsys, path.Join(root, "assets")); err != nil {
				return nil, err
			}
			continue
		}
		if err := idx.loadSection(fsys, root, e.Name()); err != nil {
			return nil, err
		}
	}

	// Cross-page references resolve only once everything is loaded.
	for _, p := range idx.pages {
		for _, rel := range p.Related {
			if _, ok := idx.pages[rel]; !ok {
				return nil, fmt.Errorf("productdocs: %s: related page %q does not exist", p.Slug, rel)
			}
		}
		// A referenced screenshot that has not been shot yet is not fatal: the
		// server must be able to start so `make docs-screenshots` can take it.
		// The unit test over the embedded content is strict, so it never
		// reaches production unnoticed; at runtime the image simply 404s.
		for _, shot := range p.Screenshots {
			if _, ok := idx.assets[shot]; !ok {
				idx.missing = append(idx.missing, p.Slug+": "+shot)
			}
		}
	}
	sort.Strings(idx.missing)

	sort.Slice(idx.sections, func(i, j int) bool {
		if idx.sections[i].Order != idx.sections[j].Order {
			return idx.sections[i].Order < idx.sections[j].Order
		}
		return idx.sections[i].Slug < idx.sections[j].Slug
	})
	return idx, nil
}

func (idx *Index) loadAssets(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("productdocs: read assets: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			return fmt.Errorf("productdocs: assets/%s: subdirectories are not allowed", e.Name())
		}
		if !assetRe.MatchString(e.Name()) {
			return fmt.Errorf("productdocs: assets/%s: name must be kebab-case with an image extension", e.Name())
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("productdocs: read assets/%s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(data)
		idx.assets[e.Name()] = &Asset{Name: e.Name(), Data: data, ETag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	return nil
}

func (idx *Index) loadSection(fsys fs.FS, root, name string) error {
	if !slugRe.MatchString(name) {
		return fmt.Errorf("productdocs: section directory %q must be kebab-case", name)
	}
	dir := path.Join(root, name)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("productdocs: read %s: %w", dir, err)
	}

	sec := &Section{Slug: name}
	var sawIndex bool
	for _, e := range entries {
		if e.IsDir() {
			return fmt.Errorf("productdocs: %s/%s: nested directories are not allowed", name, e.Name())
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			return fmt.Errorf("productdocs: %s/%s: only .md files belong in a section", name, e.Name())
		}
		raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("productdocs: read %s/%s: %w", name, e.Name(), err)
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		where := name + "/" + e.Name()

		fm, body, err := splitFrontmatter(raw)
		if err != nil {
			return fmt.Errorf("productdocs: %s: %w", where, err)
		}

		if stem == "_index" {
			sawIndex = true
			if fm.Title == "" {
				return fmt.Errorf("productdocs: %s: title is required", where)
			}
			sec.Title, sec.Summary, sec.Order, sec.Body = fm.Title, fm.Summary, fm.Order, strings.TrimSpace(body)
			continue
		}

		if !slugRe.MatchString(stem) {
			return fmt.Errorf("productdocs: %s: file name must be kebab-case", where)
		}
		p, err := newPage(name+"/"+stem, name, fm, body)
		if err != nil {
			return fmt.Errorf("productdocs: %s: %w", where, err)
		}
		idx.pages[p.Slug] = p
		sec.Pages = append(sec.Pages, p)
	}
	if !sawIndex {
		return fmt.Errorf("productdocs: %s/_index.md is missing", name)
	}
	sort.Slice(sec.Pages, func(i, j int) bool {
		if sec.Pages[i].Order != sec.Pages[j].Order {
			return sec.Pages[i].Order < sec.Pages[j].Order
		}
		return sec.Pages[i].Title < sec.Pages[j].Title
	})
	idx.sections = append(idx.sections, sec)
	return nil
}

func newPage(slug, section string, fm frontmatter, body string) (*Page, error) {
	if fm.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if fm.Summary == "" {
		return nil, fmt.Errorf("summary is required")
	}
	if len(fm.Roles) == 0 {
		return nil, fmt.Errorf("roles must name at least one role")
	}
	for _, r := range fm.Roles {
		if !ValidRoles[r] {
			return nil, fmt.Errorf("unknown role %q (valid: %s)", r, strings.Join(AllRoles, ", "))
		}
	}
	if fm.UpdatedAt == "" {
		return nil, fmt.Errorf("updated_at is required")
	}
	if _, err := time.Parse("2006-01-02", fm.UpdatedAt); err != nil {
		return nil, fmt.Errorf("updated_at %q must be YYYY-MM-DD", fm.UpdatedAt)
	}
	for _, s := range fm.Screenshots {
		if !assetRe.MatchString(s) {
			return nil, fmt.Errorf("screenshot %q must be a bare kebab-case image file name", s)
		}
	}
	// The id must be resolvable in the register; that the register actually
	// holds it is checked by the repo test, which can read outside the
	// embedded FS. Here we only reject a shape that never could resolve.
	for _, l := range fm.Limitations {
		if !slugRe.MatchString(l) {
			return nil, fmt.Errorf("limitation %q must be a kebab-case register id", l)
		}
	}
	body = strings.TrimSpace(body)
	return &Page{
		Slug:        slug,
		Section:     section,
		Title:       fm.Title,
		Summary:     fm.Summary,
		Order:       fm.Order,
		Roles:       fm.Roles,
		UpdatedAt:   fm.UpdatedAt,
		Sources:     fm.Sources,
		Limitations: fm.Limitations,
		Related:     fm.Related,
		Screenshots: fm.Screenshots,
		Body:        body,
		Stub:        body == "",
		headings:    extractHeadings(body),
	}, nil
}

// headingRe matches an ATX heading line.
var headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*\s*$`)

func extractHeadings(body string) []string {
	var out []string
	for _, m := range headingRe.FindAllStringSubmatch(body, -1) {
		out = append(out, strings.ToLower(m[1]))
	}
	return out
}

// RoleAnonymous is the role of a reader who is not signed in.
const RoleAnonymous = ""

// visible reports whether a role may read the page. An anonymous reader sees
// only pages every role may read, so narrowing a page's roles also takes it
// off the public site.
func (p *Page) visible(role string) bool {
	if role == RoleAnonymous {
		for _, r := range AllRoles {
			if !slices.Contains(p.Roles, r) {
				return false
			}
		}
		return true
	}
	return slices.Contains(p.Roles, role)
}

// Sections returns every section (pages unfiltered). For tooling and tests;
// HTTP callers use TreeFor.
func (idx *Index) Sections() []*Section { return idx.sections }

// Pages returns every page keyed by slug (unfiltered). For tooling and tests.
func (idx *Index) Pages() map[string]*Page { return idx.pages }

// TreeFor returns the navigation tree a role may see. Sections with no visible
// pages are omitted.
func (idx *Index) TreeFor(role string) []DocSection {
	var out []DocSection
	for _, s := range idx.sections {
		ds := DocSection{Slug: s.Slug, Title: s.Title, Summary: s.Summary, Order: s.Order, Body: s.Body, Pages: []DocPageMeta{}}
		for _, p := range s.Pages {
			if p.visible(role) {
				ds.Pages = append(ds.Pages, p.meta())
			}
		}
		if len(ds.Pages) > 0 {
			out = append(out, ds)
		}
	}
	if out == nil {
		out = []DocSection{}
	}
	return out
}

// PageFor returns a page if it exists AND the role may read it. Both misses
// look identical to the caller so page existence never leaks across roles.
func (idx *Index) PageFor(slug, role string) (DocPage, bool) {
	p, ok := idx.pages[slug]
	if !ok || !p.visible(role) {
		return DocPage{}, false
	}
	var sectionTitle string
	for _, s := range idx.sections {
		if s.Slug == p.Section {
			sectionTitle = s.Title
			break
		}
	}
	related := []DocPageMeta{}
	for _, rel := range p.Related {
		if rp := idx.pages[rel]; rp != nil && rp.visible(role) {
			related = append(related, rp.meta())
		}
	}
	return DocPage{
		Slug:         p.Slug,
		Title:        p.Title,
		Summary:      p.Summary,
		Section:      p.Section,
		SectionTitle: sectionTitle,
		Order:        p.Order,
		UpdatedAt:    p.UpdatedAt,
		Stub:         p.Stub,
		Body:         p.Body,
		Related:      related,
	}, true
}

// MissingScreenshots lists every `screenshots:` entry with no file in assets/,
// as "slug: name.png". Empty for a complete content set.
func (idx *Index) MissingScreenshots() []string { return idx.missing }

// Asset returns an embedded image by file name.
func (idx *Index) Asset(name string) (*Asset, bool) {
	a, ok := idx.assets[name]
	return a, ok
}

func (p *Page) meta() DocPageMeta {
	return DocPageMeta{
		Slug:      p.Slug,
		Title:     p.Title,
		Summary:   p.Summary,
		Section:   p.Section,
		Order:     p.Order,
		UpdatedAt: p.UpdatedAt,
		Stub:      p.Stub,
	}
}
