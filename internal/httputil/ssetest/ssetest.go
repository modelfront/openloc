// Package ssetest reads server-sent event streams in tests.
package ssetest

import (
	"bufio"
	"io"
	"strings"
)

// openFrame is the comment httputil.StartSSE opens every stream with.
const openFrame = ": open\n\n"

// Frames sends each frame of the stream r — its lines up to and including the
// blank line that ends it — and closes the channel when r ends. The opening
// comment is skipped; later comments, such as keepalives, are sent.
func Frames(r io.Reader) <-chan string {
	frames := make(chan string, 16)
	go func() {
		defer close(frames)
		br := bufio.NewReader(r)
		var frame strings.Builder
		opened := false
		for {
			line, err := br.ReadString('\n')
			frame.WriteString(line)
			if strings.HasSuffix(frame.String(), "\n\n") {
				if s := frame.String(); opened || s != openFrame {
					frames <- s
				}
				opened = true
				frame.Reset()
			}
			if err != nil {
				return
			}
		}
	}()
	return frames
}

// Event returns the frame's event name, or "" for a frame without one.
func Event(frame string) string { return field(frame, "event") }

// Data returns the frame's data, or "" for a frame without any.
func Data(frame string) string { return field(frame, "data") }

func field(frame, name string) string {
	for _, line := range strings.Split(frame, "\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}
