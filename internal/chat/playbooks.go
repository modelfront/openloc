package chat

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Playbooks are the assistant's skills: short Markdown
// instructions for a recurring job — a demo, onboarding — loaded on demand
// with get_playbook. A playbook may carry a saved tour that start_tour runs
// by name. Its anchors are validated like any tour step, and
// TestPlaybooksAreValid checks every file at build time.

//go:embed playbooks/*.md
var playbookFS embed.FS

type playbook struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Roles       []string `yaml:"roles"`
	// Requires names what the deployment must offer for the playbook to be
	// offered: issue_reports (it takes issue reports).
	Requires string             `yaml:"requires"`
	Tour     []playbookTourStep `yaml:"tour"`
	Body     string             `yaml:"-"`
}

// requiresIssueReports is the one value of Requires.
const requiresIssueReports = "issue_reports"

// offered reports whether the deployment offers what the playbook requires.
func (pb playbook) offered(issueReports bool) bool {
	return pb.Requires == "" || issueReports
}

type playbookTourStep struct {
	Anchor string `yaml:"anchor"`
	Title  string `yaml:"title"`
	Body   string `yaml:"body"`
	// Say is the spoken line when the tour is read aloud (TourStep.Say).
	Say  string `yaml:"say"`
	Page string `yaml:"page"`
	// Pick names a real item of the organization for a page with ids
	// (tour_picks.go); the step is left out when there is none. With Page
	// too, the step opens that page with the pick's ids.
	Pick string `yaml:"pick"`
	// Slug opens one docs page.
	Slug string `yaml:"slug"`
}

// stepPickPage is the page a picked step opens: its own Page when it names
// one that takes the same ids as the pick's page, else the pick's page.
func stepPickPage(s playbookTourStep, def tourPick) (string, error) {
	if s.Page == "" || s.Page == def.page {
		return def.page, nil
	}
	own, picked := pageByName(s.Page), pageByName(def.page)
	if own == nil || picked == nil || !slices.Equal(own.idParams, picked.idParams) {
		return "", fmt.Errorf("step %q: page %s does not take the ids of pick %s", s.Title, s.Page, s.Pick)
	}
	return s.Page, nil
}

const getPlaybookToolName = "get_playbook"

var playbooks, playbooksErr = loadPlaybooks(playbookFS)

func loadPlaybooks(fsys fs.FS) ([]playbook, error) {
	files, err := fs.Glob(fsys, "playbooks/*.md")
	if err != nil {
		return nil, err
	}
	out := make([]playbook, 0, len(files))
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		pb, err := parsePlaybook(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if want := strings.TrimSuffix(path.Base(f), ".md"); pb.Name != want {
			return nil, fmt.Errorf("%s: name %q must match the file name", f, pb.Name)
		}
		out = append(out, pb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parsePlaybook(raw []byte) (playbook, error) {
	var pb playbook
	parts := bytes.SplitN(raw, []byte("\n---\n"), 2)
	if len(parts) != 2 || !bytes.HasPrefix(parts[0], []byte("---\n")) {
		return pb, fmt.Errorf("missing frontmatter")
	}
	dec := yaml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(parts[0], []byte("---\n"))))
	dec.KnownFields(true)
	if err := dec.Decode(&pb); err != nil {
		return pb, fmt.Errorf("frontmatter: %w", err)
	}
	if pb.Name == "" || pb.Title == "" || pb.Description == "" || len(pb.Roles) == 0 {
		return pb, fmt.Errorf("name, title, description and roles are required")
	}
	if pb.Requires != "" && pb.Requires != requiresIssueReports {
		return pb, fmt.Errorf("requires %q: the only value is %s", pb.Requires, requiresIssueReports)
	}
	pb.Body = strings.TrimSpace(string(parts[1]))
	return pb, nil
}

func findPlaybook(name string) (playbook, bool) {
	for _, pb := range playbooks {
		if pb.Name == name {
			return pb, true
		}
	}
	return playbook{}, false
}

// playbooksLine lists the playbooks a role may use on this deployment, for the
// system prompt.
func playbooksLine(role string, issueReports bool) string {
	var items []string
	for _, pb := range playbooks {
		if slices.Contains(pb.Roles, role) && pb.offered(issueReports) {
			items = append(items, fmt.Sprintf("%s (%s)", pb.Name, pb.Description))
		}
	}
	if len(items) == 0 {
		return ""
	}
	return "Playbooks — load one with get_playbook before doing that job: " + strings.Join(items, "; ") + "."
}

func getPlaybookToolDef(issueReports bool) ToolDef {
	names := make([]string, 0, len(playbooks))
	for _, pb := range playbooks {
		if pb.offered(issueReports) {
			names = append(names, pb.Name)
		}
	}
	enum, _ := json.Marshal(names)
	return ToolDef{
		Name: getPlaybookToolName,
		Description: "Load a playbook: step-by-step instructions for a recurring job such as a " +
			"product demo or first-time setup. Load it before you start that job and follow it.",
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {"name": {"type": "string", "enum": %s}},
			"required": ["name"]
		}`, enum)),
	}
}

func executeGetPlaybook(args json.RawMessage, opts RunOptions) (content string, isError bool) {
	if playbooksErr != nil {
		return "playbooks are unavailable", true
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}
	pb, ok := findPlaybook(in.Name)
	if !ok || !pb.offered(opts.IssueReports) {
		return fmt.Sprintf("unknown playbook %q", in.Name), true
	}
	body := "# " + pb.Title + "\n\n" + pb.Body
	switch {
	case len(pb.Tour) > 0 && opts.SavedTours:
		body += fmt.Sprintf("\n\n(This playbook has a saved tour of %d steps: start_tour with playbook %q.)", len(pb.Tour), pb.Name)
	case len(pb.Tour) > 0:
		body += "\n\n(The Playground is off on this server, so saved tours cannot run. Give the tour with start_tour and your own steps.)"
	}
	return body, false
}

// playbookTourArgs turns a playbook's saved tour into start_tour step
// arguments, so they go through the same validation as model-written steps.
// pick resolves a step's Pick to the page ids; a step whose pick finds
// nothing is left out, and so is a step on a demo-only anchor outside the demo
// organization: every step returned can show where the tour runs.
func playbookTourArgs(name string, pick pickFunc, inDemo bool) ([]map[string]json.RawMessage, error) {
	pb, ok := findPlaybook(name)
	if !ok {
		return nil, fmt.Errorf("unknown playbook %q", name)
	}
	if len(pb.Tour) == 0 {
		return nil, fmt.Errorf("playbook %q has no saved tour", name)
	}
	out := make([]map[string]json.RawMessage, 0, len(pb.Tour))
	for _, s := range pb.Tour {
		if demoOnlyAnchor(s.Anchor) && !inDemo {
			continue
		}
		fields := map[string]any{"anchor": s.Anchor, "title": s.Title, "body": s.Body}
		if s.Say != "" {
			fields["say"] = s.Say
		}
		if s.Page != "" {
			fields["page"] = s.Page
		}
		if s.Slug != "" {
			fields[pageByName(s.Page).slugParam] = s.Slug
		}
		if s.Pick != "" {
			def, ok := tourPicks[s.Pick]
			if !ok {
				return nil, fmt.Errorf("unknown pick %q", s.Pick)
			}
			ids, found, err := pick(s.Pick)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			page, err := stepPickPage(s, def)
			if err != nil {
				return nil, err
			}
			fields["page"] = page
			for k, v := range ids {
				fields[k] = v
			}
		}
		step := map[string]json.RawMessage{}
		for k, v := range fields {
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			step[k] = b
		}
		out = append(out, step)
	}
	return out, nil
}
