package extension

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"openloc/internal/extension/exttest"
)

func TestReadPackage_MockExtension(t *testing.T) {
	p, err := ReadPackage(exttest.Zip(t, nil))
	if err != nil {
		t.Fatalf("ReadPackage: %v", err)
	}
	if p.Manifest.ID != "mockqe" || len(p.SHA256) != 64 || !bytes.HasPrefix(p.Wasm, []byte("\x00asm")) {
		t.Fatalf("package = id %q sha %q wasm %d bytes", p.Manifest.ID, p.SHA256, len(p.Wasm))
	}
}

func TestReadPackage_Rejects(t *testing.T) {
	manifest := exttest.Manifest(t)
	wasm := exttest.Wasm(t)
	cases := []struct {
		name    string
		data    func() []byte
		wantErr string
	}{
		{"not a zip", func() []byte { return []byte("PK? not really") }, "not a zip"},
		{"missing manifest", func() []byte {
			return exttest.ZipFiles(t, map[string][]byte{"extension.wasm": wasm})
		}, "manifest.json is missing"},
		{"manifest in a folder", func() []byte {
			return exttest.ZipFiles(t, map[string][]byte{"mockqe/manifest.json": manifest, "extension.wasm": wasm})
		}, "manifest.json is missing"},
		{"invalid manifest", func() []byte {
			return exttest.ZipFiles(t, map[string][]byte{"manifest.json": []byte(`{"id":"BAD"}`), "extension.wasm": wasm})
		}, "id must be"},
		{"missing wasm", func() []byte {
			return exttest.ZipFiles(t, map[string][]byte{"manifest.json": manifest})
		}, "extension.wasm is missing"},
		{"entry is not wasm", func() []byte {
			return exttest.ZipFiles(t, map[string][]byte{"manifest.json": manifest, "extension.wasm": []byte("#!/bin/sh\nrm -rf /")})
		}, "is not a WASM module"},
		{"oversized manifest", func() []byte {
			big := append([]byte(`{"pad":"`), bytes.Repeat([]byte("a"), maxManifestBytes)...)
			return exttest.ZipFiles(t, map[string][]byte{"manifest.json": append(big, '"', '}'), "extension.wasm": wasm})
		}, "manifest.json is too large"},
		{"oversized zip", func() []byte { return make([]byte, MaxZipBytes+1) }, "larger than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadPackage(tc.data())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestReadPackage_Icon(t *testing.T) {
	icon := exttest.IconPNG(t, 64, 64, false)
	p, err := ReadPackage(exttest.ZipWithIcon(t, icon))
	if err != nil {
		t.Fatalf("ReadPackage: %v", err)
	}
	if !bytes.Equal(p.Icon, icon) {
		t.Fatalf("icon = %d bytes, want the %d-byte PNG from the zip", len(p.Icon), len(icon))
	}
	if p, err := ReadPackage(exttest.Zip(t, nil)); err != nil || p.Icon != nil {
		t.Fatalf("no icon in the manifest: icon = %d bytes, err = %v", len(p.Icon), err)
	}

	big := exttest.IconPNG(t, 256, 256, true)
	if len(big) <= MaxIconBytes {
		t.Fatalf("the noisy icon is only %d bytes; it must exceed %d", len(big), MaxIconBytes)
	}
	wasm := exttest.Wasm(t)
	cases := []struct {
		name    string
		zip     []byte
		wantErr string
	}{
		{"icon file missing", exttest.ZipFiles(t, map[string][]byte{
			"manifest.json": withIcon(t, "icon.png"), "extension.wasm": wasm,
		}), "icon.png is missing"},
		{"icon in a folder", exttest.ZipFiles(t, map[string][]byte{
			"manifest.json": withIcon(t, "img/icon.png"), "extension.wasm": wasm, "img/icon.png": icon,
		}), "icon must name a .png file"},
		{"icon is not a png name", exttest.ZipFiles(t, map[string][]byte{
			"manifest.json": withIcon(t, "icon.svg"), "extension.wasm": wasm, "icon.svg": []byte("<svg/>"),
		}), "icon must name a .png file"},
		{"not a PNG", exttest.ZipWithIcon(t, []byte("<svg onload=alert(1)/>")), "icon.png is not a PNG image"},
		{"not square", exttest.ZipWithIcon(t, exttest.IconPNG(t, 64, 32, false)), "must be square"},
		{"too small", exttest.ZipWithIcon(t, exttest.IconPNG(t, 8, 8, false)), "16 to 512 pixels"},
		{"too wide", exttest.ZipWithIcon(t, exttest.IconPNG(t, 600, 600, false)), "16 to 512 pixels"},
		{"too large a file", exttest.ZipWithIcon(t, big), "at most 64 KB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadPackage(tc.zip)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// withIcon is mockqe's manifest naming icon.
func withIcon(t *testing.T, icon string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(exttest.Manifest(t), &m); err != nil {
		t.Fatal(err)
	}
	m["icon"] = icon
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
