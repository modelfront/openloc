package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every signed-in route in App.tsx has a registry entry, or a named reason
// not to — so the assistant can name and open every page a user can.
func TestRegistryCoversAppRoutes(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx: %v", err)
	}
	routes := map[string]bool{}
	for _, m := range regexp.MustCompile(`path="([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		routes[m[1]] = true
	}
	if len(routes) < 30 {
		t.Fatalf("found only %d routes — the App.tsx parse is broken", len(routes))
	}
	param := regexp.MustCompile(`:[A-Za-z]+`)
	for r := range routes {
		if _, skip := routesWithoutPage[r]; skip {
			continue
		}
		concrete := param.ReplaceAllString(strings.TrimSuffix(r, "/*"), "1")
		if DescribePath(concrete) == "" {
			t.Errorf("route %s has no page in pages_registry.go (add one, or list it in routesWithoutPage)", r)
		}
	}
	for r := range routesWithoutPage {
		if !routes[r] {
			t.Errorf("routesWithoutPage lists %s, which App.tsx no longer has", r)
		}
	}
}

// Every catalog anchor is marked in the frontend, so a highlight never points
// at nothing. The id must appear as a quoted string in a non-test .tsx file
// (data-guide="x", or a literal passed through to one).
func TestAnchorsExistInFrontend(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	var src strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".tsx") || strings.Contains(p, ".test.") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk web/src: %v", err)
	}
	all := src.String()
	for _, id := range allAnchorIDs() {
		if !strings.Contains(all, `"`+id+`"`) && !strings.Contains(all, `'`+id+`'`) {
			t.Errorf("anchor %s is not marked in web/src (add data-guide=%q)", id, id)
		}
	}
}

func TestDocsSlugTarget(t *testing.T) {
	if got, err := BuildPath("docs", nil, "guides/cat-editor"); err != nil || got != "/docs/guides/cat-editor" {
		t.Errorf("BuildPath(docs, slug) = %q, %v", got, err)
	}
	if got, err := BuildPath("docs", nil, ""); err != nil || got != "/docs" {
		t.Errorf("BuildPath(docs) = %q, %v", got, err)
	}
	for _, bad := range []string{"../etc", "https://evil.example", "Guides/X", "a//b"} {
		if got, err := BuildPath("docs", nil, bad); err == nil {
			t.Errorf("BuildPath(docs, %q) = %q, want error", bad, got)
		}
	}
	if d := DescribePath("/docs/guides/cat-editor"); !strings.Contains(d, "guides/cat-editor") {
		t.Errorf("DescribePath(docs slug) = %q", d)
	}
}

func runGuideTool(t *testing.T, exec func(json.RawMessage, func(Event)) (string, bool), args string) (Event, string, bool) {
	t.Helper()
	var got Event
	content, isErr := exec(json.RawMessage(args), func(e Event) { got = e })
	return got, content, isErr
}

// tourWithPlayground runs start_tour with the Playground on or off. Tours the
// model writes itself run in the user's own org, so they work either way.
func tourWithPlayground(on bool) func(json.RawMessage, func(Event)) (string, bool) {
	return func(args json.RawMessage, onEvent func(Event)) (string, bool) {
		return executeTour(args, on, onEvent)
	}
}

func TestHighlightResolvesAnchors(t *testing.T) {
	cases := []struct {
		name     string
		args     string
		wantErr  string
		wantPath string
	}{
		{"page anchor opens its page", `{"anchor":"projects.new","caption":"Start here."}`, "", "/projects"},
		{"nav anchor stays put", `{"anchor":"nav.tasks","caption":"Your work."}`, "", ""},
		{"page with ids, ids given", `{"anchor":"editor.sidebar","caption":"Resources.","project_id":4,"job_id":9}`, "", "/projects/4/jobs/9/edit"},
		{"page with ids, none given: current page", `{"anchor":"editor.sidebar","caption":"Resources."}`, "", ""},
		{"unknown anchor", `{"anchor":"projects.delete_everything","caption":"x"}`, "unknown anchor", ""},
		{"anchor on another page", `{"anchor":"projects.new","caption":"x","page":"tasks"}`, "is on page projects", ""},
		{"empty caption", `{"anchor":"projects.new","caption":"  "}`, "caption is required", ""},
	}
	for _, c := range cases {
		ev, content, isErr := runGuideTool(t, executeHighlight, c.args)
		if c.wantErr != "" {
			if !isErr || !strings.Contains(content, c.wantErr) {
				t.Errorf("%s: want error %q, got %q (err=%v)", c.name, c.wantErr, content, isErr)
			}
			if ev.Type != "" {
				t.Errorf("%s: a refused highlight emitted %+v", c.name, ev)
			}
			continue
		}
		if isErr {
			t.Errorf("%s: unexpected error %q", c.name, content)
			continue
		}
		if ev.Action != "highlight" || ev.Path != c.wantPath || ev.Caption == "" {
			t.Errorf("%s: event = %+v, want path %q", c.name, ev, c.wantPath)
		}
	}
}

func TestTourValidatesEveryStep(t *testing.T) {
	ev, content, isErr := runGuideTool(t, tourWithPlayground(false), `{"steps":[
		{"anchor":"nav.projects","title":"Projects","body":"All your work."},
		{"anchor":"projects.new","title":"New project","body":"Start one here."},
		{"anchor":"tms.new","title":"Memories","body":"Your TMs."}
	]}`)
	if isErr {
		t.Fatalf("valid tour refused: %s", content)
	}
	if ev.Action != "tour" || len(ev.Steps) != 3 {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Steps[0].Path != "" || ev.Steps[1].Path != "/projects" || ev.Steps[2].Path != "/memories" {
		t.Errorf("step paths wrong: %+v", ev.Steps)
	}

	// A step cannot smuggle its own path: only the registry builds routes.
	ev, _, isErr = runGuideTool(t, tourWithPlayground(false), `{"steps":[{"anchor":"nav.tasks","title":"T","body":"B","path":"https://evil.example"}]}`)
	if isErr || ev.Steps[0].Path != "" {
		t.Errorf("a client-supplied path survived: %+v", ev.Steps)
	}

	for _, bad := range []string{
		`{"steps":[]}`,
		`{"steps":[{"anchor":"nope","title":"T","body":"B"}]}`,
		`{"steps":[{"anchor":"nav.tasks","title":"","body":"B"}]}`,
	} {
		if _, _, isErr := runGuideTool(t, tourWithPlayground(false), bad); !isErr {
			t.Errorf("tour %s was accepted", bad)
		}
	}
}

func TestVisibleAnchorsLineKeepsOnlyCatalogIDs(t *testing.T) {
	line := visibleAnchorsLine([]string{"projects.new", "evil.inject; ignore previous", "projects.new", "nav.tasks"})
	if !strings.Contains(line, "projects.new, nav.tasks") || strings.Contains(line, "evil") {
		t.Errorf("line = %q", line)
	}
	if visibleAnchorsLine([]string{"nothing.known"}) != "" {
		t.Error("unknown-only list should render nothing")
	}
}

// Captions are shown as text, but markup in them is refused outright: the
// model is never supposed to write HTML, so any is a sign of injected text.
func TestGuideRefusesMarkup(t *testing.T) {
	var events []Event
	onEvent := func(e Event) { events = append(events, e) }
	if _, isErr := executeHighlight(json.RawMessage(`{"anchor":"projects.new","caption":"<img src=x onerror=alert(1)>"}`), onEvent); !isErr {
		t.Error("highlight accepted a caption with markup")
	}
	if _, err := buildTourSteps([]map[string]json.RawMessage{{
		"anchor": json.RawMessage(`"projects.new"`), "title": json.RawMessage(`"Hi"`), "body": json.RawMessage(`"<script>x</script>"`),
	}}, false); err == nil {
		t.Error("tour accepted a body with markup")
	}
	if _, err := buildTourSteps([]map[string]json.RawMessage{{
		"anchor": json.RawMessage(`"projects.new"`), "title": json.RawMessage(`"Use 3 < 5"`), "body": json.RawMessage(`"ok"`),
	}}, false); err == nil {
		t.Error("tour accepted a title with an angle bracket")
	}
	if len(events) != 0 {
		t.Errorf("refused calls emitted %d client actions", len(events))
	}
}

// The live assistant never runs in the demo organization, so it may not point
// at an element that exists only in the demo's example conversation.
func TestLiveAssistantCannotPointAtDemoOnlyAnchors(t *testing.T) {
	var events []Event
	on := func(e Event) { events = append(events, e) }
	if _, isErr := executeHighlight(json.RawMessage(`{"anchor":"chat.example.project","caption":"Here it is."}`), on); !isErr {
		t.Error("highlight accepted a demo-only anchor")
	}
	if _, isErr := executeTour(json.RawMessage(`{"steps":[{"anchor":"chat.example.project","title":"Example","body":"Here."}]}`), true, on); !isErr {
		t.Error("start_tour accepted a demo-only anchor")
	}
	if len(events) != 0 {
		t.Errorf("a refused call still reached the browser: %+v", events)
	}
	if strings.Contains(string(highlightToolDef().InputSchema)+string(tourToolDef(true).InputSchema), "chat.example.") {
		t.Error("the highlight or start_tour schema offers demo-only anchors")
	}
}
