package chat

import (
	"strings"
	"testing"
)

func TestDescribePath(t *testing.T) {
	cases := []struct {
		path string
		want string // substring; "" = context line must be omitted
	}{
		{"/projects/42/jobs/7/edit", "CAT editor for job 7 in project 42"},
		{"/projects/42", "project 42"},
		{"/", "projects list"},
		{"/tasks", "Tasks page"},
		{"/memories/3", "translation memory 3"},
		{"/terminology/5", "term base 5"},
		{"/settings", "settings"},
		// The support team's pages are described.
		{"/platform/orgs", "support team's Organizations page"},
		{"/platform/issues", "support team's Reported issues queue"},
		{"/platform/issues/12", "issue 12 in the support team's queue"},
		// Unknown, malformed, or hostile paths yield NOTHING — no junk line.
		{"/projects/42/jobs/7/edit/whatever", ""},
		{"/platform/issues/12/extra", ""},
		{"not-a-path", ""},
		{"", ""},
		{"/" + strings.Repeat("a", 400), ""},
		{"/projects/-1", ""},
	}
	for _, c := range cases {
		got := DescribePath(c.path)
		if c.want == "" && got != "" {
			t.Errorf("DescribePath(%q) = %q, want empty", c.path, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("DescribePath(%q) = %q, want substring %q", c.path, got, c.want)
		}
	}
}

func TestBuildPath(t *testing.T) {
	// Round-trip: everything BuildPath produces must be a path DescribePath
	// recognizes — the two directions of the registry can't drift apart.
	good := []struct {
		page string
		ids  map[string]int64
		want string
	}{
		{"job_editor", map[string]int64{"project_id": 42, "job_id": 7}, "/projects/42/jobs/7/edit"},
		{"project", map[string]int64{"project_id": 42}, "/projects/42"},
		{"tasks", nil, "/tasks"},
		{"projects", nil, "/projects"},
		{"term_base", map[string]int64{"term_base_id": 5}, "/terminology/5"},
	}
	for _, c := range good {
		got, err := BuildPath(c.page, c.ids, "")
		if err != nil || got != c.want {
			t.Errorf("BuildPath(%q, %v) = %q, %v; want %q", c.page, c.ids, got, err, c.want)
		}
		if DescribePath(got) == "" {
			t.Errorf("BuildPath(%q) produced %q which DescribePath does not recognize", c.page, got)
		}
	}

	bad := []struct {
		page string
		ids  map[string]int64
	}{
		{"job_editor", map[string]int64{"project_id": 42}}, // missing job_id
		{"project", map[string]int64{"project_id": 0}},     // non-positive
		{"platform_orgs", nil},                             // not whitelisted
		{"../../etc/passwd", nil},                          // hostile
		{"", nil},
	}
	for _, c := range bad {
		if got, err := BuildPath(c.page, c.ids, ""); err == nil {
			t.Errorf("BuildPath(%q, %v) = %q, want error", c.page, c.ids, got)
		}
	}
}

// Pins registry paths to the REAL routes in
// web/src/App.tsx (navigate_to("tm") → /memories/5, page context matches
// /memories/* and /projects); if a route moves, this test is the tripwire.
func TestPagesMatchRealRouterPaths(t *testing.T) {
	tm, err := BuildPath("tm", map[string]int64{"tm_id": 5}, "")
	if err != nil || tm != "/memories/5" {
		t.Errorf(`BuildPath(tm) = %q, %v; want "/memories/5" (the actual route)`, tm, err)
	}
	tms, err := BuildPath("tms", nil, "")
	if err != nil || tms != "/memories" {
		t.Errorf(`BuildPath(tms) = %q, %v; want "/memories"`, tms, err)
	}
	if d := DescribePath("/memories/5"); d == "" {
		t.Error(`DescribePath("/memories/5") = "" — TM browser context lost`)
	}
	if d := DescribePath("/projects"); d == "" {
		t.Error(`DescribePath("/projects") = "" — projects list context lost (the "/" route is just a redirect)`)
	}
}

// The support team's pages are described, never opened or pointed at: anyone
// else who lands there is sent back to the projects list.
func TestPlatformPagesAreDescribeOnly(t *testing.T) {
	for _, p := range platformPages {
		if p.name != "" || p.path != "" || len(p.anchors) > 0 {
			t.Errorf("platform page %s has a name, path or anchors; it must only be described", p.pattern)
		}
	}
	for _, name := range PageNames() {
		if strings.HasPrefix(name, "platform") {
			t.Errorf("PageNames() offers %q to navigate_to", name)
		}
	}
}
