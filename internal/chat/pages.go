package chat

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Page registry: the single source of truth for BOTH directions of
// page awareness —
//
//   browser → model: the panel sends the raw location path with each message;
//     DescribePath matches it here and becomes one system-prompt line
//     ("The user is currently viewing …"). The path is a HINT, never
//     authority: ids parsed from it grant nothing — every tool call stays
//     tenant-scoped regardless of what the client claims.
//
//   model → browser: navigate_to, highlight and start_tour take a typed
//     {page, ids} target, and BuildPath composes the concrete route
//     SERVER-side from this registry. The model never supplies a free-form
//     URL, so prompt injection can at worst bounce the user between known
//     app pages.
//
// The data lives in pages_registry.go, and the support team's pages, which
// are described but never opened, in pages_registry_platform.go.

type pageDef struct {
	// name is the identifier the model uses in navigate_to.
	name string
	// pattern matches a location path; capture groups line up with idParams,
	// or with slugParam for a page addressed by a text slug.
	pattern *regexp.Regexp
	// idParams are the numeric target fields, in capture-group order.
	idParams []string
	// slugParam is an optional text target field appended to path as "/slug".
	slugParam string
	// describe renders the system-prompt line given captured ids.
	describe string
	// path builds the concrete route with fmt verbs, in idParams order.
	path string
	// anchors are the elements of this page the assistant may point at.
	anchors []Anchor
}

// Anchor is a named UI element: the frontend marks it data-guide="<ID>".
type Anchor struct {
	ID       string
	Label    string
	Explains string
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]+(/[a-z0-9-]+)*$`)

// DescribePath renders the system-prompt context line for a location path, or
// "" when the path is unknown/garbage (the prompt simply omits the line — an
// unrecognized page must not become model-visible junk).
func DescribePath(path string) string {
	p, m := matchPath(path)
	if p == nil {
		return ""
	}
	args := make([]any, 0, len(m)-1)
	for _, id := range m[1:] {
		args = append(args, id)
	}
	if p.slugParam != "" {
		if len(m) > 1 && m[1] != "" {
			return fmt.Sprintf("%s (page %s)", p.describe, m[1])
		}
		return p.describe
	}
	return fmt.Sprintf(p.describe, args...)
}

func matchPath(path string) (*pageDef, []string) {
	raw := strings.TrimSpace(path)
	if raw == "" || len(raw) > 300 || !strings.HasPrefix(raw, "/") {
		return nil, nil
	}
	path = strings.TrimSuffix(raw, "/")
	if path == "" {
		path = "/" // raw was exactly "/" — the projects list
	}
	for _, list := range [][]pageDef{pages, platformPages} {
		for i := range list {
			if m := list[i].pattern.FindStringSubmatch(path); m != nil {
				return &list[i], m
			}
		}
	}
	return nil, nil
}

func pageByName(name string) *pageDef {
	for i := range pages {
		if pages[i].name == name {
			return &pages[i]
		}
	}
	return nil
}

// BuildPath composes a whitelisted route from the model's typed arguments.
// Unknown pages and missing/non-positive ids are caller errors the model can
// read and fix.
func BuildPath(page string, ids map[string]int64, slug string) (string, error) {
	p := pageByName(page)
	if p == nil {
		return "", fmt.Errorf("unknown page %q", page)
	}
	args := make([]any, 0, len(p.idParams))
	for _, param := range p.idParams {
		v := ids[param]
		if v <= 0 {
			return "", fmt.Errorf("page %q requires a positive %s", page, param)
		}
		args = append(args, v)
	}
	path := fmt.Sprintf(p.path, args...)
	if p.slugParam != "" && slug != "" {
		if !slugRe.MatchString(slug) {
			return "", fmt.Errorf("%s %q is not a valid slug (lowercase words joined by - and /)", p.slugParam, slug)
		}
		path += "/" + slug
	}
	return path, nil
}

// PageNames lists the navigable pages for the tool's input schema enum.
func PageNames() []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.name
	}
	return out
}

// targetParams returns every id and slug field any page takes, sorted, so the
// tool schemas are generated from the registry instead of kept by hand.
func targetParams() (ids []string, slugs []string) {
	seenID, seenSlug := map[string]bool{}, map[string]bool{}
	for _, p := range pages {
		for _, id := range p.idParams {
			if !seenID[id] {
				seenID[id] = true
				ids = append(ids, id)
			}
		}
		if p.slugParam != "" && !seenSlug[p.slugParam] {
			seenSlug[p.slugParam] = true
			slugs = append(slugs, p.slugParam)
		}
	}
	sort.Strings(ids)
	sort.Strings(slugs)
	return ids, slugs
}

// targetSchemaProperties is the JSON Schema "properties" fragment for a page
// target: page (enum) plus every id and slug field.
func targetSchemaProperties(pageDesc string) string {
	names, _ := json.Marshal(PageNames())
	ids, slugs := targetParams()
	parts := []string{fmt.Sprintf(`"page": {"type": "string", "enum": %s, "description": %q}`, names, pageDesc)}
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf(`%q: {"type": "integer", "description": "required by pages whose route has this id"}`, id))
	}
	for _, s := range slugs {
		parts = append(parts, fmt.Sprintf(`%q: {"type": "string", "description": "optional page slug, e.g. guides/cat-editor"}`, s))
	}
	return strings.Join(parts, ",\n")
}

// pageTarget is a page reference parsed from tool arguments.
type pageTarget struct {
	Page string
	IDs  map[string]int64
	Slug string
}

// parseTarget reads page, the *_id fields and the slug from raw arguments.
func parseTarget(raw map[string]json.RawMessage) (pageTarget, error) {
	t := pageTarget{IDs: map[string]int64{}}
	if v, ok := raw["page"]; ok {
		if err := json.Unmarshal(v, &t.Page); err != nil {
			return t, fmt.Errorf("page must be a string")
		}
	}
	ids, slugs := targetParams()
	for _, id := range ids {
		if v, ok := raw[id]; ok {
			var n int64
			if err := json.Unmarshal(v, &n); err != nil {
				return t, fmt.Errorf("%s must be an integer", id)
			}
			t.IDs[id] = n
		}
	}
	for _, s := range slugs {
		if v, ok := raw[s]; ok {
			if err := json.Unmarshal(v, &t.Slug); err != nil {
				return t, fmt.Errorf("%s must be a string", s)
			}
		}
	}
	return t, nil
}

// --- Anchors ----------------------------------------------------------------

// anchorPage returns the page an anchor belongs to ("" for nav anchors, which
// are on every page) and whether the anchor exists.
func anchorPage(id string) (Anchor, string, bool) {
	for _, a := range slices.Concat(navAnchors, demoAnchors) {
		if a.ID == id {
			return a, "", true
		}
	}
	for _, p := range pages {
		for _, a := range p.anchors {
			if a.ID == id {
				return a, p.name, true
			}
		}
	}
	return Anchor{}, "", false
}

// demoOnlyAnchor reports an anchor that exists only in the demo organization.
func demoOnlyAnchor(id string) bool {
	return slices.ContainsFunc(demoAnchors, func(a Anchor) bool { return a.ID == id })
}

// liveAnchorIDs lists the anchors the live assistant may point at, nav first,
// for tool schema enums: every anchor but the demo-only ones.
func liveAnchorIDs() []string {
	out := make([]string, 0, 64)
	for _, a := range navAnchors {
		out = append(out, a.ID)
	}
	for _, p := range pages {
		for _, a := range p.anchors {
			out = append(out, a.ID)
		}
	}
	return out
}

// allAnchorIDs lists every anchor, the demo-only ones included.
func allAnchorIDs() []string {
	out := liveAnchorIDs()
	for _, a := range demoAnchors {
		out = append(out, a.ID)
	}
	return out
}

// anchorCatalog is the model-facing list of anchors, grouped by page.
func anchorCatalog() string {
	var b strings.Builder
	b.WriteString("On every page (left menu and the assistant panel): ")
	for i, a := range navAnchors {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.ID)
	}
	b.WriteString(".\n")
	for _, p := range pages {
		if len(p.anchors) == 0 {
			continue
		}
		fmt.Fprintf(&b, "On page %s: ", p.name)
		for i, a := range p.anchors {
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s = %s (%s)", a.ID, a.Label, a.Explains)
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// visibleAnchorsLine renders the anchors the browser reported on screen,
// keeping only catalog ids (the list comes from the client and is a hint).
func visibleAnchorsLine(reported []string) string {
	var known []string
	seen := map[string]bool{}
	for _, id := range reported {
		if len(known) >= 40 || seen[id] {
			continue
		}
		if _, _, ok := anchorPage(id); ok {
			known = append(known, id)
			seen[id] = true
		}
	}
	if len(known) == 0 {
		return ""
	}
	return "Elements on the user's screen right now that you can point at with highlight: " +
		strings.Join(known, ", ") + "."
}
