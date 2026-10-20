package issues

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"openloc/internal/ptr"
)

// What the browser captured with a report (issue_captures): the action trail,
// the last pages, clicks and errors before the report, and the recording of
// the steps the user repeated in watch mode. Both reach the support team
// alone: never the reporter's org, and never a model. A malformed event is
// dropped, not refused, like a malformed context: the report matters more.

// The caps of a trail and a recording. The browser keeps the same; the
// server holds them.
const (
	maxTrailEvents  = 50
	maxTrailBytes   = 8 << 10
	maxTrailTarget  = 60
	maxTrailMessage = 200
	maxTrailPath    = 300

	maxRecordingEvents = 200
	maxRecordingBytes  = 64 << 10
	maxRecordingValue  = 500
	maxRecordingBody   = 2048
)

// trailEvent is one event of a trail or a recording. Each kind fills its own
// fields: page a path; click a target; api a method, path and status, and in
// a recording the start of the response body; console a message; toast a
// level and a message; input (recordings only) a target and the typed value.
type trailEvent struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Target  string `json:"target,omitempty"`
	Method  string `json:"method,omitempty"`
	Status  *int   `json:"status,omitempty"`
	Body    string `json:"body,omitempty"`
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
	// Value is set on input events only, empty for a field the user cleared.
	Value *string `json:"value,omitempty"`
}

var (
	trailMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	// trailLevels are the notice types the app's toast library has.
	trailLevels = map[string]bool{"default": true, "normal": true, "success": true, "info": true,
		"warning": true, "error": true, "loading": true, "action": true}
)

// fits trims s and checks it has 1 to max characters.
func fits(s *string, max int) bool {
	*s = strings.TrimSpace(*s)
	n := utf8.RuneCountInString(*s)
	return n > 0 && n <= max
}

// cleanRecorded is clean for an event of a recording, which also has typed
// text and response bodies.
func (e trailEvent) cleanRecorded() (trailEvent, bool) {
	switch e.Kind {
	case "input":
		at, err := time.Parse(time.RFC3339Nano, e.At)
		value := ptr.Deref(e.Value)
		out := trailEvent{At: at.UTC().Format(time.RFC3339Nano), Kind: e.Kind, Target: e.Target, Value: &value}
		return out, err == nil && fits(&out.Target, maxTrailTarget) &&
			utf8.RuneCountInString(value) <= maxRecordingValue
	case "api":
		out, ok := e.clean()
		if body := []rune(strings.TrimSpace(e.Body)); len(body) > 0 {
			out.Body = string(body[:min(len(body), maxRecordingBody)])
		}
		return out, ok
	}
	return e.clean()
}

// clean returns the event with only its kind's fields, and whether it is
// well formed.
func (e trailEvent) clean() (trailEvent, bool) {
	at, err := time.Parse(time.RFC3339Nano, e.At)
	if err != nil {
		return trailEvent{}, false
	}
	out := trailEvent{At: at.UTC().Format(time.RFC3339Nano), Kind: e.Kind}
	switch e.Kind {
	case "page":
		out.Path = maskSecrets(cleanPath(e.Path))
		return out, strings.HasPrefix(out.Path, "/") && fits(&out.Path, maxTrailPath)
	case "click":
		out.Target = e.Target
		return out, fits(&out.Target, maxTrailTarget)
	case "api":
		out.Method, out.Path, out.Status = e.Method, maskSecrets(cleanPath(e.Path)), e.Status
		return out, trailMethods[out.Method] && strings.HasPrefix(out.Path, "/api/") && fits(&out.Path, maxTrailPath) &&
			out.Status != nil && *out.Status >= 0 && *out.Status <= 599
	case "console":
		out.Message = e.Message
		return out, fits(&out.Message, maxTrailMessage)
	case "toast":
		out.Level, out.Message = e.Level, e.Message
		return out, trailLevels[out.Level] && fits(&out.Message, maxTrailMessage)
	}
	return trailEvent{}, false
}

var (
	longWord = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	digit    = regexp.MustCompile(`[0-9]`)
	letter   = regexp.MustCompile(`[A-Za-z]`)
)

// maskSecrets replaces every token-like segment of a path: a long run of
// letters and digits together is a token or an upload id, since route names
// have no digits.
func maskSecrets(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if longWord.MatchString(s) && digit.MatchString(s) && letter.MatchString(s) {
			parts[i] = ":token"
		}
	}
	return strings.Join(parts, "/")
}

// cleanTrail keeps the well-formed events of the client's trail, the newest
// 50 of them in at most 8 KB, oldest first. Nil when nothing is left.
func cleanTrail(raw string) []byte {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var in []trailEvent
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil
	}
	if len(in) > maxTrailEvents {
		in = in[len(in)-maxTrailEvents:]
	}
	events := make([]trailEvent, 0, len(in))
	for _, e := range in {
		if c, ok := e.clean(); ok {
			events = append(events, c)
		}
	}
	for len(events) > 0 {
		b, err := json.Marshal(events)
		if err == nil && len(b) <= maxTrailBytes {
			return b
		}
		events = events[1:]
	}
	return nil
}

// recording is the steps the user repeated in watch mode.
type recording struct {
	StartedAt string       `json:"started_at"`
	EndedAt   string       `json:"ended_at"`
	Events    []trailEvent `json:"events"`
}

// cleanRecording keeps the well-formed events of the client's recording, the
// first 200 of them in at most 64 KB. Nil when the recording is malformed.
func cleanRecording(raw string) []byte {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var in recording
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil
	}
	started, err1 := time.Parse(time.RFC3339Nano, in.StartedAt)
	ended, err2 := time.Parse(time.RFC3339Nano, in.EndedAt)
	if err1 != nil || err2 != nil || ended.Before(started) {
		return nil
	}
	out := recording{StartedAt: started.UTC().Format(time.RFC3339Nano), EndedAt: ended.UTC().Format(time.RFC3339Nano),
		Events: make([]trailEvent, 0, min(len(in.Events), maxRecordingEvents))}
	for _, e := range in.Events {
		if len(out.Events) == maxRecordingEvents {
			break
		}
		if c, ok := e.cleanRecorded(); ok {
			out.Events = append(out.Events, c)
		}
	}
	for {
		b, err := json.Marshal(out)
		if err == nil && len(b) <= maxRecordingBytes {
			return b
		}
		if len(out.Events) == 0 {
			return nil
		}
		out.Events = out.Events[:len(out.Events)-1]
	}
}
