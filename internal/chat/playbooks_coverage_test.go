package chat

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// stepPage is the registry page a saved tour step opens: the pick's page (or
// the step's own page with the pick's ids), else the step's page, else the
// anchor's home page. Empty for a left-menu anchor, which is on every page.
func stepPage(t *testing.T, s playbookTourStep) string {
	t.Helper()
	if s.Pick != "" {
		page, err := stepPickPage(s, tourPicks[s.Pick])
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if s.Page != "" {
		return s.Page
	}
	_, home, _ := anchorPage(s.Anchor)
	return home
}

// The saved tours show what is new in this TMS, not what every TMS has: one
// short overview, then a tour per new feature.
var featureTours = []string{
	"demo-overview",          // the basics and AI review, then where the rest is
	"tour-ai-prompts",        // AI prompts and the segment context they get
	"tour-assistant-and-mcp", // the assistant, and connecting AI tools over MCP
	"tour-dashboards",        // dashboards and the chart builder
}

func TestSavedToursAreTheFeatureTours(t *testing.T) {
	var got []string
	for _, pb := range playbooks {
		if len(pb.Tour) > 0 {
			got = append(got, pb.Name)
		}
	}
	slices.Sort(got)
	want := slices.Clone(featureTours)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("saved tours = %v, want the feature tours %v (a new tour belongs in featureTours with its reason)", got, want)
	}
}

// pageRolesFromApp reads which roles may open each page from the route
// guards in App.tsx: linguistBlock keeps linguists out, isAdmin(role) allows
// owners and admins only, and a route without a guard is open to every role.
func pageRolesFromApp(t *testing.T) map[string][]string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "App.tsx"))
	if err != nil {
		t.Fatalf("read App.tsx: %v", err)
	}
	all := []string{"owner", "admin", "project_manager", "linguist"}
	route := regexp.MustCompile(`<Route path="([^"]+)" element=\{(.*)$`)
	param := regexp.MustCompile(`:[A-Za-z]+`)
	out := map[string][]string{}
	for _, line := range strings.Split(string(src), "\n") {
		m := route.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		p, _ := matchPath(param.ReplaceAllString(strings.TrimSuffix(m[1], "/*"), "1"))
		if p == nil {
			continue
		}
		roles := all
		switch el := m[2]; {
		case strings.Contains(el, "isAdmin(role)"):
			roles = []string{"owner", "admin"}
		case strings.Contains(el, "linguistBlock("):
			roles = []string{"owner", "admin", "project_manager"}
		case strings.HasPrefix(el, "<Navigate"):
			continue
		}
		if _, seen := out[p.name]; !seen {
			out[p.name] = roles
		}
	}
	if len(out) < 30 {
		t.Fatalf("read the guards of only %d pages — the App.tsx parse is broken", len(out))
	}
	return out
}

// A saved tour never sends a role to a page the app redirects it away from.
func TestSavedToursRespectPageAccess(t *testing.T) {
	access := pageRolesFromApp(t)
	if !slices.Equal(access["org"], []string{"owner", "admin"}) || slices.Contains(access["projects"], "linguist") || !slices.Contains(access["tasks"], "linguist") {
		t.Fatalf("App.tsx guards read wrong: org %v, projects %v, tasks %v", access["org"], access["projects"], access["tasks"])
	}
	for _, pb := range playbooks {
		for i, s := range pb.Tour {
			page := stepPage(t, s)
			if page == "" {
				continue
			}
			allowed, ok := access[page]
			if !ok {
				t.Errorf("%s step %d: no App.tsx route found for page %s", pb.Name, i+1, page)
				continue
			}
			for _, r := range pb.Roles {
				if !slices.Contains(allowed, r) {
					t.Errorf("%s step %d: role %s cannot open page %s", pb.Name, i+1, r, page)
				}
			}
		}
	}
}

// A step may open another page with its pick's ids only when that page takes
// the same ids.
func TestStepPickPage(t *testing.T) {
	project := tourPick{page: "project"}
	cases := []struct {
		page    string
		want    string
		wantErr bool
	}{
		{"", "project", false},
		{"project_history", "project_history", false},
		{"job_editor", "", true}, // needs a job id the pick does not give
		{"projects", "", true},   // takes no ids at all
		{"nope", "", true},
	}
	for _, c := range cases {
		got, err := stepPickPage(playbookTourStep{Pick: "a_project", Page: c.page}, project)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("page %q: got %q, err %v", c.page, got, err)
		}
	}
}
