package chat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// suggest_replies puts up to four one-click answers under
// the assistant's message — "Next", "Skip", "Open the editor" — so a user
// walked through steps does not have to type. A chat-layer tool like
// navigate_to: validated here, shown by the browser, fire-and-forget.

const (
	repliesToolName = "suggest_replies"
	maxReplies      = 4
	maxReplyLen     = 40
)

func repliesToolDef() ToolDef {
	return ToolDef{
		Name: repliesToolName,
		Description: fmt.Sprintf("Offer the user up to %d short replies as buttons under your message "+
			"(for example \"Next\", \"Skip this step\", \"Show me in the editor\"). A click sends that text "+
			"as their next message. Use it whenever you walk the user through steps or ask them to choose, "+
			"instead of asking them to type. Replies are for choices (\"Show QA next\", \"End the demo\"); "+
			"never offer \"Next\" to move through a walkthrough — send the whole walkthrough as one "+
			"start_tour, which has its own Next button. Call it once, at the end of your answer. When an element is "+
			"highlighted, the buttons appear on its card, so keep each reply to two to four words.", maxReplies),
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"replies": {"type": "array", "minItems": 1, "maxItems": %d,
					"items": {"type": "string", "description": "at most %d characters"}}
			},
			"required": ["replies"]
		}`, maxReplies, maxReplyLen)),
	}
}

func executeSuggestReplies(args json.RawMessage, onEvent func(Event)) (content string, isError bool) {
	var in struct {
		Replies []string `json:"replies"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), true
	}
	out := make([]string, 0, len(in.Replies))
	seen := map[string]bool{}
	for _, r := range in.Replies {
		r = strings.Join(strings.Fields(r), " ")
		if r == "" || seen[r] {
			continue
		}
		if len([]rune(r)) > maxReplyLen {
			return fmt.Sprintf("reply %q is longer than %d characters", r, maxReplyLen), true
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 || len(out) > maxReplies {
		return fmt.Sprintf("give 1 to %d replies", maxReplies), true
	}
	onEvent(Event{Type: "client_action", Action: "suggest_replies", Tool: repliesToolName, Replies: out})
	return "reply buttons shown under your message", false
}
