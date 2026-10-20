package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

var knownRoles = map[string]bool{"owner": true, "admin": true, "project_manager": true, "linguist": true}

// Every embedded playbook parses, names real roles, and its saved tour passes
// the same validation start_tour applies to steps the model writes.
func TestPlaybooksAreValid(t *testing.T) {
	if playbooksErr != nil {
		t.Fatalf("load playbooks: %v", playbooksErr)
	}
	if len(playbooks) < 4 {
		t.Fatalf("want the 4 shipped playbooks, got %d", len(playbooks))
	}
	for _, pb := range playbooks {
		for _, r := range pb.Roles {
			if !knownRoles[r] {
				t.Errorf("%s: unknown role %q", pb.Name, r)
			}
		}
		if pb.Body == "" {
			t.Errorf("%s: empty body", pb.Name)
		}
		if len(pb.Tour) == 0 {
			continue
		}
		// Saved tours are read aloud; each step needs its spoken line, 30 to
		// 60 words long.
		for i, s := range pb.Tour {
			if n := len(strings.Fields(s.Say)); n < 30 || n > 60 {
				t.Errorf("%s: step %d (%s) say line has %d words, want 30 to 60", pb.Name, i+1, s.Title, n)
			}
		}
		// Outside the demo organization the served steps must validate there
		// too: a tour never reaches a step that cannot show.
		if outside, err := playbookTourArgs(pb.Name, allPicksFound, false); err != nil {
			t.Errorf("%s outside the demo: %v", pb.Name, err)
		} else if len(outside) > 0 {
			if _, err := buildTourSteps(outside, false); err != nil {
				t.Errorf("%s outside the demo: a served step cannot show: %v", pb.Name, err)
			}
		}
		// In the demo every pick resolves (fake ids): the steps must pass the
		// same validation start_tour applies, and none may be dropped.
		raws, err := playbookTourArgs(pb.Name, allPicksFound, true)
		if err != nil {
			t.Errorf("%s: %v", pb.Name, err)
			continue
		}
		steps, err := buildTourSteps(raws, true)
		if err != nil {
			t.Errorf("%s: saved tour refused: %v", pb.Name, err)
			continue
		}
		// A step that names a docs page opens that page, not the docs home.
		for i, st := range pb.Tour {
			if st.Slug != "" && !strings.HasSuffix(steps[i].Path, "/"+st.Slug) {
				t.Errorf("%s step %d: path %q does not open docs page %q", pb.Name, i+1, steps[i].Path, st.Slug)
			}
		}
		if len(steps) != len(pb.Tour) {
			t.Errorf("%s: %d steps built, want %d", pb.Name, len(steps), len(pb.Tour))
		}
		for i, st := range pb.Tour {
			if st.Pick != "" && steps[i].Path == "" {
				t.Errorf("%s step %d: a picked step must open its page", pb.Name, i+1)
			}
		}
	}
}

func TestPlaybookParsingIsStrict(t *testing.T) {
	cases := map[string]string{
		"missing frontmatter": "just text",
		"unknown key":         "---\nname: x\ntitle: X\ndescription: d\nroles: [owner]\ncolour: red\n---\nbody",
		"missing roles":       "---\nname: x\ntitle: X\ndescription: d\n---\nbody",
		"unknown requires":    "---\nname: x\ntitle: X\ndescription: d\nroles: [owner]\nrequires: billing\n---\nbody",
	}
	for name, raw := range cases {
		if _, err := parsePlaybook([]byte(raw)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
	_, err := loadPlaybooks(fstest.MapFS{
		"playbooks/right-name.md": {Data: []byte("---\nname: wrong-name\ntitle: X\ndescription: d\nroles: [owner]\n---\nbody")},
	})
	if err == nil || !strings.Contains(err.Error(), "must match the file name") {
		t.Errorf("name/file mismatch not caught: %v", err)
	}
}

func TestGetPlaybookAndRoleFilter(t *testing.T) {
	content, isErr := executeGetPlaybook(json.RawMessage(`{"name":"demo-overview"}`), RunOptions{SavedTours: true})
	if isErr || !strings.HasPrefix(content, "# Product overview demo") || !strings.Contains(content, `playbook "demo-overview"`) {
		t.Errorf("get_playbook = %q (err=%v)", content, isErr)
	}
	if _, isErr := executeGetPlaybook(json.RawMessage(`{"name":"nope"}`), RunOptions{SavedTours: true}); !isErr {
		t.Error("unknown playbook accepted")
	}

	ling := playbooksLine("linguist", false)
	if !strings.Contains(ling, "tour-assistant-and-mcp") || strings.Contains(ling, "tour-ai-prompts") {
		t.Errorf("linguist playbooks line = %q", ling)
	}
	if !strings.Contains(playbooksLine("owner", false), "tour-ai-prompts") {
		t.Error("owner should see tour-ai-prompts")
	}
}

func TestTourPlaybookOrSteps(t *testing.T) {
	if _, isErr := executeTour(json.RawMessage(`{"playbook":"demo-overview","steps":[{"anchor":"nav.tasks","title":"T","body":"B"}]}`), true, func(Event) {}); !isErr {
		t.Error("playbook and steps together were accepted")
	}
	if _, isErr := executeTour(json.RawMessage(`{"playbook":"nope"}`), true, func(Event) {}); !isErr {
		t.Error("an unknown playbook was accepted")
	}
}

// allPicksFound answers every pick with id 7 for each id the page needs.
func allPicksFound(pick string) (map[string]int64, bool, error) {
	ids := map[string]int64{}
	for _, p := range pageByName(tourPicks[pick].page).idParams {
		ids[p] = 7
	}
	return ids, true, nil
}

func TestPickedStepsAreDroppedWhenNothingFound(t *testing.T) {
	none := func(string) (map[string]int64, bool, error) { return nil, false, nil }
	raws, err := playbookTourArgs("demo-overview", none, true)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := findPlaybook("demo-overview")
	picked := 0
	for _, s := range pb.Tour {
		if s.Pick != "" {
			picked++
		}
	}
	if picked == 0 || len(raws) != len(pb.Tour)-picked {
		t.Errorf("want %d steps without the %d picked ones, got %d", len(pb.Tour)-picked, picked, len(raws))
	}
}

func TestStartTourPlaybookDefersToBrowser(t *testing.T) {
	var ev Event
	content, isErr := executeTour(json.RawMessage(`{"playbook":"demo-overview"}`), true, func(e Event) { ev = e })
	if isErr || ev.Action != "playbook_tour" || ev.Playbook != "demo-overview" || len(ev.Steps) != 0 {
		t.Errorf("event = %+v, content = %q", ev, content)
	}
}

// Saved tours run in the Playground. With it off, the assistant is neither
// offered them nor able to start one.
func TestSavedToursNeedThePlayground(t *testing.T) {
	sent := false
	if _, isErr := executeTour(json.RawMessage(`{"playbook":"demo-overview"}`), false, func(Event) { sent = true }); !isErr || sent {
		t.Errorf("a saved tour started with the Playground off (error %v, event sent %v)", isErr, sent)
	}
	if strings.Contains(string(tourToolDef(false).InputSchema), `"playbook"`) {
		t.Error("start_tour offers playbook with the Playground off")
	}
	if !strings.Contains(string(tourToolDef(true).InputSchema), `"playbook"`) {
		t.Error("start_tour lost playbook with the Playground on")
	}
	off, _ := executeGetPlaybook(json.RawMessage(`{"name":"demo-overview"}`), RunOptions{})
	on, _ := executeGetPlaybook(json.RawMessage(`{"name":"demo-overview"}`), RunOptions{SavedTours: true})
	if strings.Contains(off, "has a saved tour") || !strings.Contains(off, "Playground is off") || !strings.Contains(on, "has a saved tour") {
		t.Errorf("saved-tour note: off %q, on %q", off, on)
	}
}

// The playbook that writes an issue report is offered, to every role, only on
// a deployment that takes reports: in the prompt, the tool's list and on load.
func TestReportPlaybookNeedsIssueReports(t *testing.T) {
	const name = "report-an-issue"
	for role := range knownRoles {
		if !strings.Contains(playbooksLine(role, true), name) {
			t.Errorf("%s: not offered where reports are taken", role)
		}
		if strings.Contains(playbooksLine(role, false), name) {
			t.Errorf("%s: offered where no reports are taken", role)
		}
	}
	if strings.Contains(string(getPlaybookToolDef(false).InputSchema), name) ||
		!strings.Contains(string(getPlaybookToolDef(true).InputSchema), name) {
		t.Error("get_playbook lists it on the wrong deployment")
	}
	args := json.RawMessage(`{"name":"` + name + `"}`)
	if _, isErr := executeGetPlaybook(args, RunOptions{}); !isErr {
		t.Error("loaded where no reports are taken")
	}
	if body, isErr := executeGetPlaybook(args, RunOptions{IssueReports: true}); isErr || !strings.Contains(body, "openloc_report_issue") {
		t.Errorf("get_playbook = %q (err=%v)", body, isErr)
	}
}
