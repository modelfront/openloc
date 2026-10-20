package extension

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"io"

	"openloc/pkg/extsdk"
)

const (
	// MaxZipBytes caps an uploaded zip.
	MaxZipBytes = 32 << 20
	// maxWasmBytes caps the unpacked WASM module.
	maxWasmBytes     = 64 << 20
	maxManifestBytes = 64 << 10
	// MaxIconBytes caps the icon PNG.
	MaxIconBytes = 64 << 10
	minIconPx    = 16
	maxIconPx    = 512
)

// Package is a validated extension zip.
type Package struct {
	Manifest extsdk.Manifest
	Wasm     []byte
	// SHA256 is the hex digest of the WASM module; it keys the compile cache.
	SHA256 string
	// Icon is the manifest's icon PNG, nil when it names none.
	Icon []byte
}

// ReadPackage unpacks and validates an extension zip.
func ReadPackage(data []byte) (Package, error) {
	var p Package
	if len(data) > MaxZipBytes {
		return p, fmt.Errorf("the file is larger than %d MB", MaxZipBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return p, fmt.Errorf("the file is not a zip")
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	mf, ok := files["manifest.json"]
	if !ok {
		return p, fmt.Errorf("manifest.json is missing from the zip root")
	}
	raw, err := readZipFile(mf, maxManifestBytes)
	if err != nil {
		return p, err
	}
	if p.Manifest, err = ParseManifest(raw); err != nil {
		return p, err
	}
	wf, ok := files[p.Manifest.Entry]
	if !ok {
		return p, fmt.Errorf("%s is missing from the zip root", p.Manifest.Entry)
	}
	if p.Wasm, err = readZipFile(wf, maxWasmBytes); err != nil {
		return p, err
	}
	if !bytes.HasPrefix(p.Wasm, []byte("\x00asm")) {
		return p, fmt.Errorf("%s is not a WASM module", p.Manifest.Entry)
	}
	sum := sha256.Sum256(p.Wasm)
	p.SHA256 = hex.EncodeToString(sum[:])
	if p.Manifest.Icon != "" {
		f, ok := files[p.Manifest.Icon]
		if !ok {
			return p, fmt.Errorf("%s is missing from the zip root", p.Manifest.Icon)
		}
		if p.Icon, err = readIcon(f); err != nil {
			return p, err
		}
	}
	return p, nil
}

// readIcon reads the icon and checks it is a square PNG of a sensible size.
// Only the header is decoded.
func readIcon(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > MaxIconBytes {
		return nil, fmt.Errorf("%s must be at most %d KB", f.Name, MaxIconBytes>>10)
	}
	b, err := readZipFile(f, MaxIconBytes)
	if err != nil {
		return nil, err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s is not a PNG image", f.Name)
	}
	if cfg.Width != cfg.Height || cfg.Width < minIconPx || cfg.Width > maxIconPx {
		return nil, fmt.Errorf("%s must be square, %d to %d pixels wide", f.Name, minIconPx, maxIconPx)
	}
	return b, nil
}

// readZipFile reads one entry, refusing more than limit bytes whatever the
// header claims.
func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%s is too large", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", f.Name)
	}
	return b, nil
}

// load returns the module, for Call.Wasm.
func (p Package) load(context.Context) ([]byte, error) { return p.Wasm, nil }
