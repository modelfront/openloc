package appearance

import (
	"fmt"
	"regexp"
	"strings"
)

// Go is the single source of truth for the palette model:
// the MCP theme tools read built-in anchors server-side, and the TypeScript
// constant is GENERATED from this file — see gen_ts_palettes.go and `make types`.
//
// The frontend gets a bundled constant rather than a fetch: palettes are
// resolved synchronously before React's first paint to avoid a colour flash, so
// they cannot come over the wire.

const (
	DefaultLightPalette = "paper"
	DefaultDarkPalette  = "dim"

	// MaxCustomPalettes bounds what one user can store. The column is JSONB on
	// a per-user row, so this is about keeping the row (and the MCP tool
	// response) sane rather than about storage cost.
	MaxCustomPalettes = 24
	MaxNameLen        = 40
)

// AnchorKeys are the 7 editable roles a palette carries. Order is display order
// in the editor, and the generator emits it for the frontend to reuse.
var AnchorKeys = []string{"bg", "surface", "elevated", "border", "text", "muted", "accent"}

// AnchorLabels describe each role for humans — surfaced in the palette editor
// and in the MCP tool description, so a model choosing colours knows what each
// one paints.
var AnchorLabels = map[string]string{
	"bg":       "Page background — the furthest-back surface",
	"surface":  "Cards, sidebar, header",
	"elevated": "Hover states, chips, secondary surfaces",
	"border":   "Dividers and input borders",
	"text":     "Primary text",
	"muted":    "Secondary / subdued text",
	"accent":   "Buttons, links, active states",
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Palette is one theme. Anchors stays a map rather than a struct because it is
// the persisted JSONB shape and the exact-key validation below depends on
// seeing extra keys — a struct would silently drop them instead of rejecting.
type Palette struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
	// Builtin is omitted for customs so stored custom palettes carry no
	// builtin key.
	Builtin bool              `json:"builtin,omitempty"`
	Anchors map[string]string `json:"anchors"`
}

// BuiltinPalettes ships with the app and cannot be edited or deleted. The
// generator writes them out to TS so the two can never disagree.
var BuiltinPalettes = []Palette{
	{
		ID: "paper", Name: "Paper", Mode: "light", Builtin: true,
		Anchors: map[string]string{
			"bg": "#faf9f7", "surface": "#f5f4f1", "elevated": "#ebe9e4", "border": "#ddd9d2",
			"text": "#1c1917", "muted": "#78716c", "accent": "#3b82f6",
		},
	},
	{
		ID: "light-default", Name: "Default light", Mode: "light", Builtin: true,
		Anchors: map[string]string{
			"bg": "#ffffff", "surface": "#f9fafb", "elevated": "#f3f4f6", "border": "#e5e7eb",
			"text": "#111827", "muted": "#6b7280", "accent": "#3b82f6",
		},
	},
	{
		ID: "mist", Name: "Mist", Mode: "light", Builtin: true,
		Anchors: map[string]string{
			"bg": "#f8fafc", "surface": "#f1f5f9", "elevated": "#e2e8f0", "border": "#cbd5e1",
			"text": "#0f172a", "muted": "#64748b", "accent": "#3b82f6",
		},
	},
	{
		ID: "dim", Name: "Dim", Mode: "dark", Builtin: true,
		Anchors: map[string]string{
			"bg": "#09090b", "surface": "#18181b", "elevated": "#27272a", "border": "#3f3f46",
			"text": "#fafafa", "muted": "#a1a1aa", "accent": "#3b82f6",
		},
	},
	{
		ID: "black-true", Name: "True black", Mode: "dark", Builtin: true,
		Anchors: map[string]string{
			"bg": "#000000", "surface": "#0a0a0a", "elevated": "#171717", "border": "#262626",
			"text": "#fafafa", "muted": "#a3a3a3", "accent": "#3b82f6",
		},
	},
	{
		ID: "graphite", Name: "Graphite", Mode: "dark", Builtin: true,
		Anchors: map[string]string{
			"bg": "#030712", "surface": "#111827", "elevated": "#1f2937", "border": "#374151",
			"text": "#f3f4f6", "muted": "#9ca3af", "accent": "#3b82f6",
		},
	},
}

// BuiltinByID returns the built-in with this id, or false. Used to stop a
// custom palette shadowing a built-in id, and to let the MCP tools hand a
// built-in's anchors to a caller that wants to derive from it.
func BuiltinByID(id string) (Palette, bool) {
	for _, p := range BuiltinPalettes {
		if p.ID == id {
			return p, true
		}
	}
	return Palette{}, false
}

// ValidatePalette checks one palette in isolation. Shared by the HTTP handler
// and the MCP tools so the two paths cannot drift.
//
// Returns a user-facing message: it reaches both an HTTP 400 body and an MCP
// tool error, so it must not leak internals.
func ValidatePalette(p *Palette) error {
	p.Name = strings.TrimSpace(p.Name)
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" || p.Name == "" {
		return fmt.Errorf("each custom palette needs an id and a name")
	}
	if len([]rune(p.Name)) > MaxNameLen {
		return fmt.Errorf("palette name is too long")
	}
	if p.Mode != "light" && p.Mode != "dark" {
		return fmt.Errorf("palette mode must be light or dark")
	}
	// Exact count, not just "the 7 are present": an extra key would otherwise
	// round-trip into storage unnoticed.
	if len(p.Anchors) != len(AnchorKeys) {
		return fmt.Errorf("palette is missing colors")
	}
	for _, k := range AnchorKeys {
		v, ok := p.Anchors[k]
		if !ok || !hexRe.MatchString(v) {
			return fmt.Errorf("palette colors must be #rrggbb hex")
		}
	}
	return nil
}

// ValidateCustomPalettes validates the whole set a user is about to store:
// per-palette rules, the count bound, unique ids, and no shadowing of a
// built-in id.
func ValidateCustomPalettes(customs []Palette) error {
	if len(customs) > MaxCustomPalettes {
		return fmt.Errorf("too many custom palettes")
	}
	seen := map[string]bool{}
	for i := range customs {
		p := &customs[i]
		// Customs are never builtin, whatever the caller claimed — otherwise a
		// client could mark its own palette undeletable.
		p.Builtin = false
		if err := ValidatePalette(p); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate custom palette id")
		}
		seen[p.ID] = true
		if _, isBuiltin := BuiltinByID(p.ID); isBuiltin {
			return fmt.Errorf("%q is a built-in palette id", p.ID)
		}
	}
	return nil
}
