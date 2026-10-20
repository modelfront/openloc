package chat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Pause tools ask the user to do something in the browser before the
// assistant goes on: add a screenshot of the page, or repeat the steps while
// the browser records them for the support team. The model only decides
// whether to ask; the user starts every capture, and a recording always holds
// the same things. The turn ends with the call: the model is not called
// again, and the browser sends the outcome as the user's next message.

const (
	screenshotToolName = "ask_for_screenshot"
	repeatToolName     = "ask_to_repeat"
	maxPauseReason     = 120
)

func pauseReasonSchema(what string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"reason": {"type": "string", "description": "one line the user reads next to the button: why you ask for %s (at most %d characters)"}
		},
		"required": ["reason"]
	}`, what, maxPauseReason))
}

func screenshotToolDef() ToolDef {
	return ToolDef{
		Name: screenshotToolName,
		Description: "Ask the user for one screenshot of the page they are on, when the problem they report " +
			"is visual and on their screen now. Write one sentence asking for it, then call this tool; the " +
			"turn ends there. The user sees an Add screenshot button and decides: their next message brings " +
			"the screenshot, which you see in that message only, or says they skipped it. Never ask for a " +
			"screenshot of something that happened earlier.",
		InputSchema: pauseReasonSchema("the screenshot"),
	}
}

func repeatToolDef() ToolDef {
	return ToolDef{
		Name: repeatToolName,
		Description: "Ask the user to repeat the steps that cause the problem while the browser records them " +
			"for the support team. Only when the user's words, the page and the data still leave it unclear, " +
			"and the user can make it happen again. Write one sentence asking for it, then call this tool; " +
			"the turn ends there. The user starts the recording and stops it. The recording goes to the " +
			"support team with the report, never to you: their next message only says whether one is attached.",
		InputSchema: pauseReasonSchema("the recording"),
	}
}

// pauseTools are offered on a deployment that takes issue reports, but not on
// the support team's pages: they only help write a report, and a capture
// there would carry other organizations' data.
func pauseTools(opts RunOptions) []ToolDef {
	if !opts.IssueReports || supportPage(opts.PagePath) {
		return nil
	}
	return []ToolDef{screenshotToolDef(), repeatToolDef()}
}

// supportPages show every organization's data: the reports under /platform,
// the LLM calls under /debug/llm. The browser keeps the same list.
var supportPages = []string{"/platform", "/debug/llm"}

// supportPage reports one of the support team's pages. The app's routes match
// without regard to case or escapes, so this check does too.
func supportPage(pagePath string) bool {
	if p, err := url.PathUnescape(pagePath); err == nil {
		pagePath = p
	}
	pagePath = strings.ToLower(pagePath)
	for _, page := range supportPages {
		if pagePath == page || strings.HasPrefix(pagePath, page+"/") {
			return true
		}
	}
	return false
}

func isPauseTool(name string) bool {
	return name == screenshotToolName || name == repeatToolName
}

// pauseNotes are what the model reads after it asked.
var pauseNotes = map[string]string{
	screenshotToolName: "The user now sees an Add screenshot button. Stop here: their next message brings " +
		"the screenshot or says they skipped it.",
	repeatToolName: "The user now sees a Start recording button. Stop here: their next message says whether a " +
		"recording is attached to the report. You will not see the recording.",
}

// pauseActions are the client actions the browser turns into a button.
var pauseActions = map[string]string{
	screenshotToolName: "ask_screenshot",
	repeatToolName:     "ask_repeat",
}

// executePause validates the reason and puts the button in front of the
// user. One request per turn: a second one is refused.
func executePause(name string, args json.RawMessage, alreadyPaused bool, onEvent func(Event)) (content string, isError bool) {
	if alreadyPaused {
		return "you already asked the user for something in this message; ask for one thing at a time", true
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}
	reason := strings.Join(strings.Fields(in.Reason), " ")
	if reason == "" || utf8.RuneCountInString(reason) > maxPauseReason {
		return fmt.Sprintf("give a reason of 1 to %d characters", maxPauseReason), true
	}
	onEvent(Event{Type: "client_action", Action: pauseActions[name], Tool: name, Reason: reason})
	return pauseNotes[name], false
}
