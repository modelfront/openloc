package parser

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
	"time"
)

// Zip helpers shared by the XLSX and PPTX tests.

// rewriteZip copies a zip archive (an XLSX or PPTX), letting edit replace
// any entry. edit returns ok=false to keep the entry as it is.
func rewriteZip(t *testing.T, content []byte, edit func(w *zip.Writer, f *zip.File) (ok bool)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if edit(w, f) {
			continue
		}
		if err := w.Copy(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withDeclaredSizes keeps each named part's compressed bytes but makes its
// header claim the given unzipped size.
func withDeclaredSizes(t *testing.T, content []byte, sizes map[string]uint64) []byte {
	t.Helper()
	for name := range sizes {
		if !hasPart(t, content, name) {
			t.Fatalf("archive has no part %s", name)
		}
	}
	return rewriteZip(t, content, func(w *zip.Writer, f *zip.File) bool {
		size, ok := sizes[f.Name]
		if !ok {
			return false
		}
		raw, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		hdr := f.FileHeader
		hdr.UncompressedSize64 = size
		fw, err := w.CreateRaw(&hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(fw, raw); err != nil {
			t.Fatal(err)
		}
		return true
	})
}

func hasPart(t *testing.T, content []byte, name string) bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

// callGuarded runs fn with a time limit and turns an escaped panic into a test
// failure, so a crafted file that crashes or hangs the parser fails cleanly.
func callGuarded(t *testing.T, fn func([]byte) error, content []byte) error {
	t.Helper()
	type result struct {
		err   error
		panic any
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- result{panic: rec}
			}
		}()
		done <- result{err: fn(content)}
	}()
	select {
	case r := <-done:
		if r.panic != nil {
			t.Fatalf("panic escaped the parser: %v", r.panic)
		}
		return r.err
	case <-time.After(10 * time.Second):
		t.Fatal("parser did not return within 10s")
		return nil
	}
}

// readZipEntry returns an entry's unzipped content.
func readZipEntry(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeZipEntry adds an entry with the given content.
func writeZipEntry(t *testing.T, w *zip.Writer, name, body string) {
	t.Helper()
	fw, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}
