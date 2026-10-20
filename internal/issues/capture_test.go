package issues

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/ptr"
)

func decodeTrail(t *testing.T, b []byte) []trailEvent {
	t.Helper()
	var out []trailEvent
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("stored trail %s: %v", b, err)
	}
	return out
}

// The server keeps only well-formed events, each with its kind's fields, and
// no query string or token in a path.
func TestCleanTrailKeepsWellFormedEvents(t *testing.T) {
	raw := `[
		{"at":"2026-10-16T10:00:00Z","kind":"page","path":"/projects/12?q=secret text","target":"extra"},
		{"at":"2026-10-16T10:00:01Z","kind":"click","target":"  Save  "},
		{"at":"2026-10-16T10:00:02Z","kind":"api","method":"POST","path":"/api/chat/uploads/3f1c9a2e-0b4d-4c7e-9a51-6d2f8e7b1c04?x=1","status":500},
		{"at":"2026-10-16T10:00:03Z","kind":"console","message":"TypeError: x is undefined"},
		{"at":"2026-10-16T10:00:04Z","kind":"toast","level":"error","message":"Could not save."},
		{"at":"2026-10-16T10:00:05Z","kind":"keystroke","message":"hello"},
		{"at":"yesterday","kind":"click","target":"Late"},
		{"at":"2026-10-16T10:00:06Z","kind":"api","method":"TRACE","path":"/api/x","status":500},
		{"at":"2026-10-16T10:00:07Z","kind":"api","method":"GET","path":"/elsewhere","status":404},
		{"at":"2026-10-16T10:00:08Z","kind":"click","target":"` + strings.Repeat("x", 61) + `"},
		{"at":"2026-10-16T10:00:09Z","kind":"page","path":"/reset-password/abc"},
		{"at":"2026-10-16T10:00:10Z","kind":"api","method":"GET","path":"/api/auth/notification-preferences","status":502}
	]`
	got := decodeTrail(t, cleanTrail(raw))
	want := []trailEvent{
		{At: "2026-10-16T10:00:00Z", Kind: "page", Path: "/projects/12"},
		{At: "2026-10-16T10:00:01Z", Kind: "click", Target: "Save"},
		{At: "2026-10-16T10:00:02Z", Kind: "api", Method: "POST", Path: "/api/chat/uploads/:token", Status: ptr.To(500)},
		{At: "2026-10-16T10:00:03Z", Kind: "console", Message: "TypeError: x is undefined"},
		{At: "2026-10-16T10:00:04Z", Kind: "toast", Level: "error", Message: "Could not save."},
		{At: "2026-10-16T10:00:09Z", Kind: "page", Path: "/reset-password/:token"},
		{At: "2026-10-16T10:00:10Z", Kind: "api", Method: "GET", Path: "/api/auth/notification-preferences", Status: ptr.To(502)},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("cleanTrail =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	if strings.Contains(string(cleanTrail(raw)), "secret text") {
		t.Error("a query string survived")
	}
}

// At most the newest 50 events, in at most 8 KB: the oldest go first.
func TestCleanTrailCaps(t *testing.T) {
	var events []string
	for i := 0; i < 80; i++ {
		events = append(events, fmt.Sprintf(`{"at":"2026-10-16T10:00:%02dZ","kind":"click","target":"Button %d"}`, i%60, i))
	}
	got := decodeTrail(t, cleanTrail("["+strings.Join(events, ",")+"]"))
	if len(got) != maxTrailEvents || got[0].Target != "Button 30" || got[len(got)-1].Target != "Button 79" {
		t.Errorf("kept %d events, %q … %q", len(got), got[0].Target, got[len(got)-1].Target)
	}

	events = nil
	for i := 0; i < 50; i++ {
		events = append(events, fmt.Sprintf(`{"at":"2026-10-16T10:00:00Z","kind":"console","message":"%03d %s"}`, i, strings.Repeat("e", 190)))
	}
	b := cleanTrail("[" + strings.Join(events, ",") + "]")
	got = decodeTrail(t, b)
	if len(b) > maxTrailBytes || len(got) == 0 || !strings.HasPrefix(got[len(got)-1].Message, "049") {
		t.Errorf("%d bytes, %d events, the newest kept: %v", len(b), len(got), len(got) > 0 && strings.HasPrefix(got[len(got)-1].Message, "049"))
	}

	for _, raw := range []string{"", "  ", "not json", `{"kind":"page"}`, `[{"kind":"page","path":"/"}]`} {
		if b := cleanTrail(raw); b != nil {
			t.Errorf("cleanTrail(%q) = %s, want nothing", raw, b)
		}
	}
}

// A recording keeps typed text and response bodies within their caps, the
// first 200 events, in at most 64 KB.
func TestCleanRecording(t *testing.T) {
	long := strings.Repeat("b", 3000)
	raw := `{"started_at":"2026-10-16T10:00:00Z","ended_at":"2026-10-16T10:01:00Z","events":[
		{"at":"2026-10-16T10:00:01Z","kind":"input","target":"Search","value":"export job 9"},
		{"at":"2026-10-16T10:00:02Z","kind":"api","method":"POST","path":"/api/x","status":500,"body":"` + long + `"},
		{"at":"2026-10-16T10:00:03Z","kind":"input","target":"Notes","value":"` + strings.Repeat("v", 501) + `"},
		{"at":"2026-10-16T10:00:04Z","kind":"keystroke","value":"x"},
		{"at":"2026-10-16T10:00:05Z","kind":"click","target":"Export"},
		{"at":"2026-10-16T10:00:06Z","kind":"input","target":"Search","value":""}
	]}`
	var got recording
	if err := json.Unmarshal(cleanRecording(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 4 || *got.Events[0].Value != "export job 9" || len([]rune(got.Events[1].Body)) != maxRecordingBody ||
		got.Events[2].Target != "Export" || got.Events[2].Value != nil {
		t.Errorf("recording = %+v", got)
	}
	// A cleared field keeps its empty value, so the page shows what was typed: nothing.
	if !strings.Contains(string(cleanRecording(raw)), `"target":"Search","value":""`) {
		t.Errorf("a cleared field lost its value: %s", cleanRecording(raw))
	}

	// At most the first 200 events, even when they fit in 64 KB.
	var small []string
	for i := 0; i < 300; i++ {
		small = append(small, fmt.Sprintf(`{"at":"2026-10-16T10:00:00Z","kind":"click","target":"Field %d"}`, i))
	}
	if err := json.Unmarshal(cleanRecording(`{"started_at":"2026-10-16T10:00:00Z","ended_at":"2026-10-16T10:03:00Z","events":[`+strings.Join(small, ",")+`]}`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != maxRecordingEvents || got.Events[len(got.Events)-1].Target != "Field 199" {
		t.Errorf("kept %d events, last %q", len(got.Events), got.Events[len(got.Events)-1].Target)
	}

	var events []string
	for i := 0; i < 300; i++ {
		events = append(events, fmt.Sprintf(`{"at":"2026-10-16T10:00:00Z","kind":"input","target":"Field %d","value":"%s"}`, i, strings.Repeat("v", 400)))
	}
	b := cleanRecording(`{"started_at":"2026-10-16T10:00:00Z","ended_at":"2026-10-16T10:03:00Z","events":[` + strings.Join(events, ",") + `]}`)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(b) > maxRecordingBytes || len(got.Events) == 0 || got.Events[0].Target != "Field 0" {
		t.Errorf("%d bytes, %d events, first %q", len(b), len(got.Events), got.Events[0].Target)
	}

	for _, raw := range []string{"", "nope", `{"events":[]}`, `{"started_at":"2026-10-16T10:01:00Z","ended_at":"2026-10-16T10:00:00Z","events":[]}`} {
		if b := cleanRecording(raw); b != nil {
			t.Errorf("cleanRecording(%q) = %s, want nothing", raw, b)
		}
	}
}
