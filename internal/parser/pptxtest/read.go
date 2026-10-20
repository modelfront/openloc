package pptxtest

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// ReadParts unzips content and returns every entry's content by name.
func ReadParts(tb testing.TB, content []byte) map[string]string {
	tb.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		tb.Fatalf("open zip: %v", err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			tb.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			tb.Fatalf("read %s: %v", f.Name, err)
		}
		parts[f.Name] = string(b)
	}
	return parts
}

// ReadPart returns one entry of content, unzipped.
func ReadPart(tb testing.TB, content []byte, name string) string {
	tb.Helper()
	part, ok := ReadParts(tb, content)[name]
	if !ok {
		tb.Fatalf("no part %s", name)
	}
	return part
}
