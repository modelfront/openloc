package parser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"unicode/utf16"

	"openloc/internal/errtrack"
)

// Zip safety shared by the OOXML formats (XLSX, PPTX). An OOXML file is a zip
// of XML parts, so each parser refuses the same three things before it reads
// a part: an OLE compound file instead of a zip, a zip it cannot open, and
// parts whose declared unzipped size goes past maxOOXMLUnzipped.

// maxOOXMLUnzipped caps the total declared unzipped size of the parts a
// parser reads. excelize's own default is 16 GB.
const maxOOXMLUnzipped = 256 << 20

// cfbSignature starts an OLE compound file: the container Office uses for
// password-protected OOXML files and for the pre-2007 binary formats.
var cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// ooxmlErrors are the sentinels one OOXML format reports, so each format
// keeps its own errors while sharing the checks.
type ooxmlErrors struct {
	encrypted error
	tooLarge  error
	malformed error
	// panicAction is the errtrack action of a recovered panic.
	panicAction string
}

// openOOXMLZip refuses an OLE compound file and opens content as a zip.
func openOOXMLZip(content []byte, errs ooxmlErrors) (*zip.Reader, error) {
	if bytes.HasPrefix(content, cfbSignature) {
		return nil, errs.encrypted
	}
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errs.malformed, err)
	}
	return zr, nil
}

// unzipBudget counts the declared unzipped sizes of the parts a parser reads
// against a limit. Go's zip reader never returns more bytes than an
// entry declares, so reserving an entry before reading it bounds the read.
type unzipBudget struct {
	left     uint64
	tooLarge error
}

func newUnzipBudget(limit uint64, tooLarge error) *unzipBudget {
	return &unzipBudget{left: limit, tooLarge: tooLarge}
}

// reserve takes f's declared unzipped size from the budget, or returns the
// too-large error when it does not fit.
func (b *unzipBudget) reserve(f *zip.File) error {
	if f.UncompressedSize64 > b.left {
		return b.tooLarge
	}
	b.left -= f.UncompressedSize64
	return nil
}

// errRecoveredPanic marks an error that a recovered panic produced, so tests
// can tell a parser that refused a file from one that crashed on it.
var errRecoveredPanic = errors.New("recovered panic")

// ooxmlPanicError reports rec, a value recovered from a panic, and sets *err
// to an error wrapping errs.malformed and errRecoveredPanic. recover only
// works when the deferred function calls it itself, so each format's deferred
// recover function calls recover and hands the value here.
func ooxmlPanicError(ctx context.Context, rec any, op string, errs ooxmlErrors, err *error) {
	errtrack.CapturePanic(ctx, rec, "parser", errs.panicAction)
	*err = fmt.Errorf("%s: %w: %w (panic: %v)", op, errs.malformed, errRecoveredPanic, rec)
}

// cfbHasStream reports whether an OLE compound file names a stream called
// name. Directory entries store names as UTF-16LE, so the encoded name
// appearing in the file is what tells a legacy binary document (a
// "PowerPoint Document" stream) from an encrypted OOXML package (an
// "EncryptedPackage" stream).
func cfbHasStream(content []byte, name string) bool {
	units := utf16.Encode([]rune(name))
	encoded := make([]byte, 0, 2*len(units))
	for _, u := range units {
		encoded = append(encoded, byte(u), byte(u>>8))
	}
	return bytes.Contains(content, encoded)
}
