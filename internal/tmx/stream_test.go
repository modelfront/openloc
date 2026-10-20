package tmx

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// countingReader counts the bytes read through it.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

func bigTMX(units int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tmx version="1.4"><header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="t" creationtoolversion="1" o-tmf="t"/><body>`)
	for i := 0; i < units; i++ {
		fmt.Fprintf(&b, `<tu tuid="%d"><tuv xml:lang="en"><seg>Source %d</seg></tuv><tuv xml:lang="de"><seg>Ziel %d</seg></tuv></tu>`, i, i, i)
	}
	b.WriteString(`</body></tmx>`)
	return b.String()
}

// Entries arrive in batches of at most the batch size, the first one long
// before the end of the file is read, and every entry arrives once in order.
func TestStreamEntries_BoundedBatchesBeforeEOF(t *testing.T) {
	const units, size = 5000, 100
	doc := bigTMX(units)
	cr := &countingReader{r: strings.NewReader(doc)}

	var got, calls int
	readAtFirst := -1
	err := StreamEntries(cr, size, func(batch []Entry) error {
		if readAtFirst < 0 {
			readAtFirst = cr.read
		}
		if len(batch) == 0 || len(batch) > size {
			t.Fatalf("batch of %d entries, want 1..%d", len(batch), size)
		}
		for _, e := range batch {
			if want := fmt.Sprintf("Source %d", got); e.SourceText != want {
				t.Fatalf("entry %d = %q, want %q", got, e.SourceText, want)
			}
			got++
		}
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamEntries: %v", err)
	}
	if got != units || calls != units/size {
		t.Errorf("%d entries in %d calls, want %d in %d", got, calls, units, units/size)
	}
	if readAtFirst >= len(doc)/2 {
		t.Errorf("first batch after reading %d of %d bytes, want it while streaming", readAtFirst, len(doc))
	}
}

// An error from the callback stops the stream and comes back unchanged.
func TestStreamEntries_StopsOnCallbackError(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	err := StreamEntries(strings.NewReader(bigTMX(50)), 10, func([]Entry) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("err = %v after %d calls, want stop after 1", err, calls)
	}
}

// Broken XML after some entries: the entries before it are delivered, then
// the stream fails.
func TestStreamEntries_DeliversEntriesBeforeBrokenXML(t *testing.T) {
	doc := strings.TrimSuffix(bigTMX(35), `</body></tmx>`) + `<tu><tuv xml:lang="en"><seg>broken`
	got := 0
	err := StreamEntries(strings.NewReader(doc), 10, func(batch []Entry) error {
		got += len(batch)
		return nil
	})
	if err == nil {
		t.Fatal("want an error for the broken XML")
	}
	if got != 35 {
		t.Errorf("%d entries delivered before the break, want 35", got)
	}
}
