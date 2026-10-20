package appearance

import (
	"os"
	"path/filepath"
	"testing"
)

// generatedPath is the checked-in TypeScript constant, relative to this package.
const generatedPath = "../../web/src/lib/builtin-palettes.generated.ts"

// TestGeneratedTypeScriptIsCurrent is the guard that makes "Go is the source of
// truth" actually true.
//
// The frontend needs built-in palettes as a BUNDLED constant — it resolves a
// palette synchronously before first paint to avoid a colour flash — so they
// can't be fetched from Go at runtime. That leaves a generated file, and a
// generated file that nobody checks goes stale: edit palettes.go, forget
// `make types`, and the two silently disagree about what "Paper" looks like.
//
// Anti-tautology: change any built-in hex in palettes.go without regenerating
// and this test fails with the exact remedy.
func TestGeneratedTypeScriptIsCurrent(t *testing.T) {
	onDisk, err := os.ReadFile(filepath.Clean(generatedPath))
	if err != nil {
		t.Fatalf("read %s: %v (run `make types`)", generatedPath, err)
	}
	if got, want := string(onDisk), RenderTypeScript(); got != want {
		t.Errorf("%s is stale — run `make types`.\n"+
			"on disk is %d bytes, generator produces %d",
			generatedPath, len(got), len(want))
	}
}

// Every built-in must itself satisfy the rules we impose on user palettes —
// otherwise the app ships a palette the editor would reject, and "duplicate a
// built-in then tweak it" breaks.
func TestBuiltinsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for i := range BuiltinPalettes {
		p := BuiltinPalettes[i] // copy: ValidatePalette trims in place
		if err := ValidatePalette(&p); err != nil {
			t.Errorf("built-in %q is invalid: %v", p.ID, err)
		}
		if !p.Builtin {
			t.Errorf("built-in %q is missing the builtin flag", p.ID)
		}
		if seen[p.ID] {
			t.Errorf("duplicate built-in id %q", p.ID)
		}
		seen[p.ID] = true
	}
	// The two defaults must exist and be in the mode they default for,
	// otherwise resolvePalette's fallback has nothing to fall back to.
	light, ok := BuiltinByID(DefaultLightPalette)
	if !ok || light.Mode != "light" {
		t.Errorf("default light palette %q missing or not light", DefaultLightPalette)
	}
	dark, ok := BuiltinByID(DefaultDarkPalette)
	if !ok || dark.Mode != "dark" {
		t.Errorf("default dark palette %q missing or not dark", DefaultDarkPalette)
	}
}

func TestValidateCustomPalettes(t *testing.T) {
	valid := func() Palette {
		return Palette{ID: "mine", Name: "Mine", Mode: "dark", Anchors: map[string]string{
			"bg": "#000000", "surface": "#111111", "elevated": "#222222", "border": "#333333",
			"text": "#ffffff", "muted": "#888888", "accent": "#3b82f6",
		}}
	}

	t.Run("accepts a well-formed palette", func(t *testing.T) {
		if err := ValidateCustomPalettes([]Palette{valid()}); err != nil {
			t.Fatalf("rejected a valid palette: %v", err)
		}
	})

	t.Run("strips a claimed builtin flag", func(t *testing.T) {
		p := valid()
		p.Builtin = true // a client claiming its own palette is built-in
		set := []Palette{p}
		if err := ValidateCustomPalettes(set); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Left set, the palette would render as undeletable in the picker.
		if set[0].Builtin {
			t.Error("builtin flag survived on a custom palette")
		}
	})

	t.Run("rejects shadowing a built-in id", func(t *testing.T) {
		p := valid()
		p.ID = DefaultDarkPalette
		if err := ValidateCustomPalettes([]Palette{p}); err == nil {
			t.Error("a custom palette took a built-in id")
		}
	})

	t.Run("rejects duplicate ids", func(t *testing.T) {
		if err := ValidateCustomPalettes([]Palette{valid(), valid()}); err == nil {
			t.Error("duplicate ids accepted")
		}
	})

	t.Run("rejects an extra anchor key", func(t *testing.T) {
		p := valid()
		p.Anchors["rogue"] = "#ffffff"
		// Exact-count matters: an unknown key would round-trip into JSONB and
		// sit there forever.
		if err := ValidateCustomPalettes([]Palette{p}); err == nil {
			t.Error("extra anchor key accepted")
		}
	})

	t.Run("rejects non-hex and shorthand hex", func(t *testing.T) {
		for _, bad := range []string{"navy", "#fff", "#12345", "rgb(0,0,0)", ""} {
			p := valid()
			p.Anchors["accent"] = bad
			if err := ValidateCustomPalettes([]Palette{p}); err == nil {
				t.Errorf("accepted %q as a colour", bad)
			}
		}
	})

	t.Run("enforces the count bound", func(t *testing.T) {
		many := make([]Palette, MaxCustomPalettes+1)
		for i := range many {
			p := valid()
			p.ID = string(rune('a'+i%26)) + string(rune('0'+i/26))
			many[i] = p
		}
		if err := ValidateCustomPalettes(many); err == nil {
			t.Error("count bound not enforced")
		}
	})
}
