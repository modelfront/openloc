package chat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Chat-layer page tools. None of them
// is an MCP tool: they act on the user's browser, which external MCP clients
// and PAT agents don't have. The loop appends them to the model's tool list,
// validates the arguments against the page registry, and emits a
// client_action event. They are fire-and-forget.

const (
	navigateToolName  = "navigate_to"
	highlightToolName = "highlight"
	tourToolName      = "start_tour"

	maxTourSteps    = 16
	maxCaptionLen   = 400
	maxTourTitleLen = 80
	maxTourBodyLen  = 400
	maxTourSayLen   = 600
)

// TourStep is one stop of a guided tour. Path is empty when the step stays on
// the page the user is on.
type TourStep struct {
	Path   string `json:"path,omitempty"`
	Anchor string `json:"anchor"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// Say is what the voice reads instead of Body: the same point as full,
	// spoken sentences. Never shown.
	Say string `json:"say,omitempty"`
}

// Tour and highlight text is plain text. The browser escapes it anyway;
// refusing < and > also stops markup the model copied from a document.
const errNoMarkup = "write plain text: no < or > characters"

func hasMarkup(s string) bool { return strings.ContainsAny(s, "<>") }

func navigateToolDef() ToolDef {
	return ToolDef{
		Name: navigateToolName,
		Description: "Open a page of the OpenLoc app in the user's browser — use it to take " +
			"the user to the thing you're talking about (a job's editor, a project, a term " +
			"base, a docs page). Say what you're opening and why. The navigation happens " +
			"immediately; you don't need to wait or verify.",
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {%s},
			"required": ["page"]
		}`, targetSchemaProperties("which app page to open"))),
	}
}

func highlightToolDef() ToolDef {
	anchors, _ := json.Marshal(liveAnchorIDs())
	return ToolDef{
		Name: highlightToolName,
		Description: "Point at one element of the app: the user's screen dims except that element, " +
			"with your caption next to it. Use it when you explain where something is or what " +
			"it does (\"where do I create a project?\"). If the element is on another page, " +
			"pass page (and its ids) and the app opens it first. Only for ONE element: to show two " +
			"or more, never highlight them one per message — send them all at once with start_tour.\n\nElements you can point at:\n" + anchorCatalog(),
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"anchor": {"type": "string", "enum": %s, "description": "the element to point at"},
				"caption": {"type": "string", "description": "one or two short sentences shown next to the element"},
				%s
			},
			"required": ["anchor", "caption"]
		}`, anchors, targetSchemaProperties("page to open first, when the element is not on the current page"))),
	}
}

// sayDescription is the writing-for-the-ear guide the model follows for a
// step's spoken line.
const sayDescription = "what the voice says for this step, when tours are read aloud. Write it for the ear, " +
	"like a person giving a live demo: full sentences with verbs, talking to 'you', one idea, " +
	"30 to 60 words. Say where things are ('on the right', 'at the top'). Link to the previous step " +
	"('Next, let's look at...'); greet on the first step, wrap up on the last. No symbols, " +
	"shortcuts written as keys, abbreviations or quoted labels: 'Control Enter', 'translation memory'."

func tourToolDef(savedTours bool) ToolDef {
	anchors, _ := json.Marshal(liveAnchorIDs())
	withTours := []string{}
	for _, pb := range playbooks {
		if len(pb.Tour) > 0 {
			withTours = append(withTours, pb.Name)
		}
	}
	saved, _ := json.Marshal(withTours)
	// Saved tours run in the Playground; with it off only steps are offered.
	how := "Pass steps."
	playbookProp := ""
	if savedTours {
		how = "Pass either steps, or playbook to run a playbook's saved tour."
		playbookProp = fmt.Sprintf(`"playbook": {"type": "string", "enum": %s, "description": "run this playbook's saved tour instead of steps"},`, saved)
	}
	return ToolDef{
		Name: tourToolName,
		Description: fmt.Sprintf("Start a guided tour: up to %d steps, each pointing at one element "+
			"with a title and a short explanation. Plan the whole walkthrough first and send every "+
			"step in this one call: the user moves with Next and Back without waiting for you, and "+
			"can close it at any time. If a step needs a real item (a job, a project, a memory), look "+
			"up its ids with the list tools BEFORE calling, and pass them on that step. A step may name a page (and its ids); the app opens it when "+
			"the user reaches that step. Use it for \"show me around\", onboarding and demos. "+
			"The elements are the same as for highlight. "+how, maxTourSteps),
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				%s
				"steps": {
					"type": "array",
					"maxItems": %d,
					"items": {
						"type": "object",
						"properties": {
							"anchor": {"type": "string", "enum": %s},
							"title": {"type": "string", "description": "a few words"},
							"body": {"type": "string", "description": "one to three short sentences, shown on the card"},
							"say": {"type": "string", "description": %q},
							%s
						},
						"required": ["anchor", "title", "body"]
					}
				}
			}
		}`, playbookProp, maxTourSteps, anchors, sayDescription, targetSchemaProperties("page this step is on, when it is not the previous step's page"))),
	}
}

// executeNavigate validates the model's arguments against the page registry
// and emits the client action. Invalid input is a model-visible error, not a
// crash — the model reads it and corrects the call.
func executeNavigate(args json.RawMessage, onEvent func(Event)) (content string, isError bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Sprintf("invalid navigate arguments: %v", err), true
	}
	t, err := parseTarget(raw)
	if err != nil {
		return err.Error(), true
	}
	path, err := BuildPath(t.Page, t.IDs, t.Slug)
	if err != nil {
		return err.Error(), true
	}
	onEvent(Event{Type: "client_action", Action: "navigate", Path: path, Tool: navigateToolName})
	return fmt.Sprintf("navigation to %s triggered in the user's browser", path), false
}

// resolveAnchor checks an anchor and works out the route to open first.
// A page anchor on a page without ids resolves its own route; a page with ids
// needs the model to pass them, or the highlight runs on the current page.
// A demo-only anchor resolves only for a tour run in the demo organization.
func resolveAnchor(anchorID string, t pageTarget, inDemo bool) (path string, err error) {
	_, home, ok := anchorPage(anchorID)
	if !ok {
		return "", fmt.Errorf("unknown anchor %q — use one from the list in the tool description", anchorID)
	}
	if demoOnlyAnchor(anchorID) && !inDemo {
		return "", fmt.Errorf("anchor %q exists only in the Playground — use one from the list in the tool description", anchorID)
	}
	if t.Page != "" && home != "" && t.Page != home {
		return "", fmt.Errorf("anchor %q is on page %s, not %s", anchorID, home, t.Page)
	}
	page := t.Page
	if page == "" {
		page = home
	}
	if page == "" {
		return "", nil // nav anchor: on every page
	}
	p := pageByName(page)
	if len(p.idParams) > 0 && len(t.IDs) == 0 {
		return "", nil // needs ids the model did not give: use the current page
	}
	return BuildPath(page, t.IDs, t.Slug)
}

func executeHighlight(args json.RawMessage, onEvent func(Event)) (content string, isError bool) {
	var raw map[string]json.RawMessage
	var in struct {
		Anchor  string `json:"anchor"`
		Caption string `json:"caption"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Sprintf("invalid highlight arguments: %v", err), true
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid highlight arguments: %v", err), true
	}
	anchor, caption := in.Anchor, strings.TrimSpace(in.Caption)
	if caption == "" || len(caption) > maxCaptionLen {
		return fmt.Sprintf("caption is required, at most %d characters", maxCaptionLen), true
	}
	if hasMarkup(caption) {
		return errNoMarkup, true
	}
	t, err := parseTarget(raw)
	if err != nil {
		return err.Error(), true
	}
	// The live assistant never runs in the demo organization: its chat is
	// refused there, and the demo shows an example conversation instead.
	path, err := resolveAnchor(anchor, t, false)
	if err != nil {
		return err.Error(), true
	}
	onEvent(Event{Type: "client_action", Action: "highlight", Tool: highlightToolName,
		Path: path, Anchor: anchor, Caption: caption})
	if path != "" {
		return fmt.Sprintf("opening %s and highlighting %s in the user's browser", path, anchor), false
	}
	return fmt.Sprintf("highlighting %s on the user's current page (shown only if it is on screen)", anchor), false
}

func executeTour(args json.RawMessage, savedTours bool, onEvent func(Event)) (content string, isError bool) {
	var in struct {
		Playbook string                       `json:"playbook"`
		Steps    []map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid tour arguments: %v", err), true
	}
	if in.Playbook != "" {
		if !savedTours {
			return "saved tours are off on this server (the Playground is off); pass your own steps instead", true
		}
		if len(in.Steps) > 0 {
			return "pass either steps or playbook, not both", true
		}
		// The browser loads the saved tour from GET /api/chat/playbooks,
		// where its picks resolve to this organization's items — the same
		// steps the "Take the tour" buttons run.
		pb, ok := findPlaybook(in.Playbook)
		if !ok {
			return fmt.Sprintf("unknown playbook %q", in.Playbook), true
		}
		if len(pb.Tour) == 0 {
			return fmt.Sprintf("playbook %q has no saved tour", in.Playbook), true
		}
		onEvent(Event{Type: "client_action", Action: "playbook_tour", Tool: tourToolName, Playbook: pb.Name})
		return fmt.Sprintf("the saved tour of %q started in the user's browser", pb.Name), false
	}
	steps, err := buildTourSteps(in.Steps, false)
	if err != nil {
		return err.Error(), true
	}
	onEvent(Event{Type: "client_action", Action: "tour", Tool: tourToolName, Steps: steps})
	return fmt.Sprintf("tour with %d steps started in the user's browser; the user moves through it at their own pace", len(steps)), false
}

// buildTourSteps validates tour steps against the anchor catalog and builds
// each step's route from the page registry. start_tour and the playbook list
// endpoint both use it, so a saved tour is checked the same way on both paths.
// inDemo says the tour runs in the demo organization, the only place a
// demo-only anchor exists.
func buildTourSteps(raws []map[string]json.RawMessage, inDemo bool) ([]TourStep, error) {
	if len(raws) == 0 || len(raws) > maxTourSteps {
		return nil, fmt.Errorf("a tour needs 1 to %d steps", maxTourSteps)
	}
	steps := make([]TourStep, 0, len(raws))
	for i, raw := range raws {
		var s TourStep
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("step %d: %v", i+1, err)
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("step %d: %v", i+1, err)
		}
		s.Path = ""
		s.Title, s.Body, s.Say = strings.TrimSpace(s.Title), strings.TrimSpace(s.Body), strings.TrimSpace(s.Say)
		if s.Title == "" || len(s.Title) > maxTourTitleLen || s.Body == "" || len(s.Body) > maxTourBodyLen {
			return nil, fmt.Errorf("step %d: title (max %d) and body (max %d characters) are required",
				i+1, maxTourTitleLen, maxTourBodyLen)
		}
		if len(s.Say) > maxTourSayLen {
			return nil, fmt.Errorf("step %d: say is at most %d characters", i+1, maxTourSayLen)
		}
		if hasMarkup(s.Title) || hasMarkup(s.Body) || hasMarkup(s.Say) {
			return nil, fmt.Errorf("step %d: %s", i+1, errNoMarkup)
		}
		t, err := parseTarget(raw)
		if err != nil {
			return nil, fmt.Errorf("step %d: %v", i+1, err)
		}
		if s.Path, err = resolveAnchor(s.Anchor, t, inDemo); err != nil {
			return nil, fmt.Errorf("step %d: %v", i+1, err)
		}
		steps = append(steps, s)
	}
	return steps, nil
}
