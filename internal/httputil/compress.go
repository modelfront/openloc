package httputil

import (
	"fmt"
	"net/http"

	"github.com/CAFxX/httpcompression"
	"github.com/CAFxX/httpcompression/contrib/klauspost/gzip"
	"github.com/CAFxX/httpcompression/contrib/klauspost/zstd"
	kzstd "github.com/klauspost/compress/zstd"
)

// compressMinSize is the smallest body worth compressing; below it the
// encoding overhead outweighs the saving.
const compressMinSize = 1024

// compressTypes are the only content types compressed. Event streams are not
// in the list, so live updates are never held back by a compressor; already
// compressed downloads (DOCX, ZIP, images) are not either.
var compressTypes = []string{
	"application/json",
	"application/javascript",
	"application/xml",
	"image/svg+xml",
	"text/css",
	"text/csv",
	"text/html",
	"text/javascript",
	"text/markdown",
	"text/plain",
	"text/xml",
}

// Compress returns middleware that compresses text responses with zstd or
// gzip, whichever the client accepts, preferring zstd. Its settings are
// constants, so an error is a programming mistake and panics at startup.
func Compress() func(http.Handler) http.Handler {
	mw, err := newCompress()
	if err != nil {
		panic(err)
	}
	return mw
}

func newCompress() (func(http.Handler) http.Handler, error) {
	gz, err := gzip.New(gzip.Options{Level: gzip.DefaultCompression})
	if err != nil {
		return nil, fmt.Errorf("gzip compressor: %w", err)
	}
	// Browsers refuse zstd frames whose window is larger than 8 MB.
	zs, err := zstd.New(kzstd.WithEncoderLevel(kzstd.SpeedDefault), kzstd.WithWindowSize(8<<20))
	if err != nil {
		return nil, fmt.Errorf("zstd compressor: %w", err)
	}
	return httpcompression.Adapter(
		httpcompression.Compressor(zstd.Encoding, 1, zs),
		httpcompression.Compressor(gzip.Encoding, 0, gz),
		httpcompression.Prefer(httpcompression.PreferServer),
		httpcompression.MinSize(compressMinSize),
		httpcompression.ContentTypes(compressTypes, false),
	)
}
