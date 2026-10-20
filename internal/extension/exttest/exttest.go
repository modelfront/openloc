// Package exttest builds the mock QE test extension
// (internal/extension/testdata/mockqe) for tests in any package.
package exttest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once     sync.Once
	wasm     []byte
	manifest []byte
	buildErr error
)

// srcDir is testdata/mockqe next to this package.
func srcDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", "mockqe")
}

// build compiles mockqe once per process. The module is cached in the user
// cache dir by a digest of its sources and the SDK, so reruns skip the build.
func build() {
	dir := srcDir()
	manifest, buildErr = os.ReadFile(filepath.Join(dir, "manifest.json"))
	if buildErr != nil {
		return
	}
	h := sha256.New()
	for _, f := range []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "..", "..", "..", "..", "pkg", "extsdk", "contract.go"),
		filepath.Join(dir, "..", "..", "..", "..", "pkg", "extsdk", "guest.go"),
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			buildErr = err
			return
		}
		h.Write(b)
	}
	h.Write([]byte(runtime.Version()))
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	out := filepath.Join(cacheDir, "openloc-exttest", hex.EncodeToString(h.Sum(nil))[:16]+".wasm")
	if b, err := os.ReadFile(out); err == nil {
		wasm = b
		return
	}
	if buildErr = os.MkdirAll(filepath.Dir(out), 0o755); buildErr != nil {
		return
	}
	tmp := out + ".tmp"
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", tmp, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		buildErr = fmt.Errorf("%w: %s", err, b)
		return
	}
	if buildErr = os.Rename(tmp, out); buildErr != nil {
		return
	}
	wasm, buildErr = os.ReadFile(out)
}

// Wasm returns the compiled mock QE module.
func Wasm(t testing.TB) []byte {
	t.Helper()
	once.Do(build)
	if buildErr != nil {
		t.Fatalf("build mockqe: %v", buildErr)
	}
	return wasm
}

// Manifest returns mockqe's manifest.json.
func Manifest(t testing.TB) []byte {
	t.Helper()
	Wasm(t)
	return manifest
}

// Zip returns an extension zip with the given manifest (nil = mockqe's own).
func Zip(t testing.TB, manifestJSON []byte) []byte {
	t.Helper()
	w := Wasm(t)
	if manifestJSON == nil {
		manifestJSON = manifest
	}
	return ZipFiles(t, map[string][]byte{"manifest.json": manifestJSON, "extension.wasm": w})
}

// ZipFiles returns a zip holding the given files.
func ZipFiles(t testing.TB, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// IconPNG returns a w×h PNG. noisy fills it with random pixels, which barely
// compress, to make a large file.
func IconPNG(t testing.TB, w, h int, noisy bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: 0x20, G: 0x60, B: 0xc0, A: 0xff}
			if noisy {
				c = color.NRGBA{R: uint8(r.UintN(256)), G: uint8(r.UintN(256)), B: uint8(r.UintN(256)), A: 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ZipWithIcon returns mockqe's zip with "icon": "icon.png" in the manifest
// and icon as that file.
func ZipWithIcon(t testing.TB, icon []byte) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(Manifest(t), &m); err != nil {
		t.Fatal(err)
	}
	m["icon"] = "icon.png"
	mj, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return ZipFiles(t, map[string][]byte{"manifest.json": mj, "extension.wasm": Wasm(t), "icon.png": icon})
}
