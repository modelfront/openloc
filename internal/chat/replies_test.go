package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSuggestReplies(t *testing.T) {
	var ev Event
	content, isErr := executeSuggestReplies(json.RawMessage(`{"replies":["Next","  Skip   this step ","Next",""]}`), func(e Event) { ev = e })
	if isErr {
		t.Fatalf("refused: %s", content)
	}
	if ev.Action != "suggest_replies" || strings.Join(ev.Replies, "|") != "Next|Skip this step" {
		t.Errorf("event = %+v (want trimmed, deduplicated, empties dropped)", ev)
	}
	for _, bad := range []string{
		`{"replies":[]}`,
		`{"replies":["a","b","c","d","e"]}`,
		`{"replies":["` + strings.Repeat("x", 41) + `"]}`,
	} {
		ev = Event{}
		if _, isErr := executeSuggestReplies(json.RawMessage(bad), func(e Event) { ev = e }); !isErr || ev.Type != "" {
			t.Errorf("%s: want a refusal and no event", bad)
		}
	}
}
