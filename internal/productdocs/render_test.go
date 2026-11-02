package productdocs

import (
	"strings"
	"testing"
)

func TestRenderHTML(t *testing.T) {
	body := "## Confirm a segment\n\nPress **Ctrl+Enter**. See [roles](/docs/reference/roles).\n\n" +
		"![Editor](assets/cat-editor.png)\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n<script>alert(1)</script>\n"
	got, err := RenderHTML(body, func(h string) string { return "X" + h })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h2 id="confirm-a-segment">Confirm a segment</h2>`,
		`<strong>Ctrl+Enter</strong>`,
		`<a href="X/docs/reference/roles">roles</a>`,
		`<img src="Xassets/cat-editor.png" alt="Editor">`,
		`<td>1</td>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("raw HTML kept:\n%s", got)
	}
}

func TestRenderHTML_EveryPage(t *testing.T) {
	for name, load := range map[string]func() (*Index, error){"content": Default, "apicontent": API} {
		idx, err := load()
		if err != nil {
			t.Fatal(err)
		}
		for slug, p := range idx.Pages() {
			out, err := RenderHTML(p.Body, nil)
			if err != nil {
				t.Errorf("%s/%s: %v", name, slug, err)
			}
			if !p.Stub && strings.TrimSpace(out) == "" {
				t.Errorf("%s/%s: empty HTML", name, slug)
			}
		}
	}
}
