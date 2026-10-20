package ssetest

import (
	"strings"
	"testing"
)

func TestFrames_SplitsAtTheBlankLine(t *testing.T) {
	stream := "event: connected\ndata: {\"unread\":2}\n\n: keepalive\n\nevent: notification\ndata: {}\n\nevent: cut"
	var got []string
	for f := range Frames(strings.NewReader(stream)) {
		got = append(got, f)
	}
	want := []string{"event: connected\ndata: {\"unread\":2}\n\n", ": keepalive\n\n", "event: notification\ndata: {}\n\n"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("frames = %q, want %q (an unfinished frame is not sent)", got, want)
	}
	if names := []string{Event(got[0]), Event(got[1]), Event(got[2])}; strings.Join(names, ",") != "connected,,notification" {
		t.Fatalf("event names = %q", names)
	}
	if data := []string{Data(got[0]), Data(got[1])}; data[0] != `{"unread":2}` || data[1] != "" {
		t.Fatalf("data = %q", data)
	}
}
