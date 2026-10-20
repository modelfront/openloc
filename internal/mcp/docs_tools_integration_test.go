// Documentation tools over the real protocol: list_docs,
// search_docs, get_doc_page.
//
// The docs pages are global content that every signed-in role may read, so the
// guarantee here is the opposite of a filter: the tools return the same tree,
// the same pages and the same hits whatever the caller's role, and a slug that
// does not exist is a plain error.
//
// The role filter itself still lives in productdocs and still runs on every
// call; it is covered there against a fixture whose pages DO narrow roles
// (TestTreeFor_FiltersByRole, TestPageFor_HidesPagesTheRoleMayNotRead), so a
// future page that restricts its `roles:` is not untested.
package mcp_test

import (
	"sort"
	"strings"
	"testing"

	"openloc/internal/productdocs"
)

type docTreeOut struct {
	Sections []productdocs.DocSection `json:"sections"`
}

type docSearchOut struct {
	Hits []productdocs.DocSearchHit `json:"hits"`
}

func TestIntegrationMCPListDocs_SameForEveryRole(t *testing.T) {
	f := newFixture(t)

	var manager docTreeOut
	if isErr, text := callTool(t, connect(t, f.url, f.tokenA), "openloc_list_docs", map[string]any{}, &manager); isErr {
		t.Fatalf("list_docs (owner): %s", text)
	}
	if len(manager.Sections) < 4 {
		t.Fatalf("owner sees %d sections, want the four Diátaxis sections", len(manager.Sections))
	}

	var linguist docTreeOut
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_list_docs", map[string]any{}, &linguist); isErr {
		t.Fatalf("list_docs (linguist): %s", text)
	}

	// Every page reaches every role: the two trees must be identical, not merely
	// overlapping. Comparing the whole slug set is what catches a page that
	// quietly narrows its roles: again.
	managerSlugs, linguistSlugs := treeSlugs(manager), treeSlugs(linguist)
	if strings.Join(managerSlugs, ",") != strings.Join(linguistSlugs, ",") {
		t.Fatalf("owner and linguist see different pages\n owner:    %v\n linguist: %v", managerSlugs, linguistSlugs)
	}
	for _, want := range []string{"guides/cat-editor", "guides/translation-memory", "concepts/quality-estimation"} {
		if !treeHas(linguist, want) {
			t.Fatalf("linguist tree is missing %s", want)
		}
	}
}

func treeSlugs(tree docTreeOut) []string {
	var out []string
	for _, s := range tree.Sections {
		for _, p := range s.Pages {
			out = append(out, p.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func treeHas(tree docTreeOut, slug string) bool {
	for _, s := range tree.Sections {
		for _, p := range s.Pages {
			if p.Slug == slug {
				return true
			}
		}
	}
	return false
}

func TestIntegrationMCPGetDocPage(t *testing.T) {
	f := newFixture(t)
	ling := connect(t, f.url, f.tokenLingA)

	var page productdocs.DocPage
	if isErr, text := callTool(t, ling, "openloc_get_doc_page",
		map[string]any{"slug": "guides/cat-editor"}, &page); isErr {
		t.Fatalf("get_doc_page: %s", text)
	}
	if page.Title != "The CAT editor" || page.Stub || !strings.Contains(page.Body, "##") || page.UpdatedAt == "" {
		t.Fatalf("page payload: title=%q stub=%v updated=%q body=%d bytes", page.Title, page.Stub, page.UpdatedAt, len(page.Body))
	}

	// A page about work a linguist does not administer is still readable by one:
	// the docs are reference material, not a permission surface.
	if isErr, text := callTool(t, ling, "openloc_get_doc_page",
		map[string]any{"slug": "guides/translation-memory"}, &page); isErr {
		t.Fatalf("linguist get_doc_page(guides/translation-memory): %s", text)
	}

	// A slug that does not exist fails the same way for both roles, so the error
	// text can never become a hint about what a caller is not allowed to see.
	isErrLing, textLing := callTool(t, ling, "openloc_get_doc_page", map[string]any{"slug": "guides/does-not-exist"}, nil)
	isErrOwner, textOwner := callTool(t, connect(t, f.url, f.tokenA), "openloc_get_doc_page", map[string]any{"slug": "guides/does-not-exist"}, nil)
	if !isErrLing || !isErrOwner {
		t.Fatalf("expected both calls to fail: %v/%v", isErrLing, isErrOwner)
	}
	if textLing != textOwner {
		t.Fatalf("error text differs by role: %q vs %q", textLing, textOwner)
	}
}

func TestIntegrationMCPSearchDocs(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)

	var out docSearchOut
	if isErr, text := callTool(t, s, "openloc_search_docs", map[string]any{"query": "confirm segment"}, &out); isErr {
		t.Fatalf("search_docs: %s", text)
	}
	if len(out.Hits) == 0 || out.Hits[0].Slug == "" || out.Hits[0].Snippet == "" {
		t.Fatalf("expected ranked hits with snippets, got %+v", out.Hits)
	}

	// A blank query is a caller mistake and says so.
	if isErr, text := callTool(t, s, "openloc_search_docs", map[string]any{"query": "  "}, nil); !isErr || !strings.Contains(text, "query is required") {
		t.Fatalf("blank query: isErr=%v text=%q", isErr, text)
	}

	// Search reaches the same pages for every role. A manager-topic query is the
	// interesting case: it must reach a linguist too.
	const query = "translation memory profile"
	if isErr, text := callTool(t, s, "openloc_search_docs", map[string]any{"query": query}, &out); isErr {
		t.Fatalf("owner search: %s", text)
	}
	if len(out.Hits) == 0 {
		t.Fatalf("owner search for %q found nothing", query)
	}
	var ling docSearchOut
	if isErr, text := callTool(t, connect(t, f.url, f.tokenLingA), "openloc_search_docs", map[string]any{"query": query}, &ling); isErr {
		t.Fatalf("linguist search: %s", text)
	}
	if strings.Join(hitSlugs(out), ",") != strings.Join(hitSlugs(ling), ",") {
		t.Fatalf("search results differ by role\n owner:    %v\n linguist: %v", hitSlugs(out), hitSlugs(ling))
	}
}

func hitSlugs(out docSearchOut) []string {
	var slugs []string
	for _, h := range out.Hits {
		slugs = append(slugs, h.Slug)
	}
	sort.Strings(slugs)
	return slugs
}
