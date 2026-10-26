package common

import (
	"archive/zip"
	"errors"
	"openloc/internal/parsers/common/cfbtest"
	"testing"
)

// The budget is a running total of declared sizes: one part may use all of
// it, and the part that would go past it is refused, however the total is
// split. A size near the top of uint64 must not wrap around.
func TestUnzipBudget(t *testing.T) {
	tooLarge := errors.New("too large")
	part := func(size uint64) *zip.File {
		return &zip.File{FileHeader: zip.FileHeader{UncompressedSize64: size}}
	}
	cases := []struct {
		name  string
		sizes []uint64
		ok    int // parts that fit
	}{
		{"exactly the cap", []uint64{MaxOOXMLUnzipped}, 1},
		{"one byte over", []uint64{MaxOOXMLUnzipped + 1}, 0},
		{"two halves", []uint64{MaxOOXMLUnzipped / 2, MaxOOXMLUnzipped / 2}, 2},
		{"two halves and a byte", []uint64{MaxOOXMLUnzipped / 2, MaxOOXMLUnzipped/2 + 1}, 1},
		{"after the cap is used", []uint64{MaxOOXMLUnzipped, 1}, 1},
		{"near uint64 max", []uint64{1, 1 << 63, 1}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewUnzipBudget(MaxOOXMLUnzipped, tooLarge)
			fit := 0
			for _, size := range tc.sizes {
				if err := b.Reserve(part(size)); err != nil {
					if !errors.Is(err, tooLarge) {
						t.Fatalf("err = %v", err)
					}
					break
				}
				fit++
			}
			if fit != tc.ok {
				t.Errorf("%d parts fit, want %d", fit, tc.ok)
			}
		})
	}
}

// An encrypted OOXML file and a pre-2007 binary file are both compound files;
// the stream names tell them apart.
func TestCFBHasStream(t *testing.T) {
	if !CFBHasStream(cfbtest.CFBFile("PowerPoint Document"), "PowerPoint Document") {
		t.Error("stream name not found")
	}
	if CFBHasStream(cfbtest.CFBFile("EncryptedPackage"), "PowerPoint Document") {
		t.Error("found a stream the file does not name")
	}
}
