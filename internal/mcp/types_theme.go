package mcp

// GetAppearanceIn takes nothing: the subject is always the calling user.
type GetAppearanceIn struct{}

// ThemeAnchors is the 7 colour roles as named fields rather than a map, so the
// tool schema tells a model exactly what it is choosing. Converted to the
// storage map by toAnchors.
type ThemeAnchors struct {
	Bg       string `json:"bg" jsonschema:"page background, the furthest-back surface. #rrggbb hex."`
	Surface  string `json:"surface" jsonschema:"cards, sidebar, header. #rrggbb hex."`
	Elevated string `json:"elevated" jsonschema:"hover states, chips, secondary surfaces. #rrggbb hex."`
	Border   string `json:"border" jsonschema:"dividers and input borders. #rrggbb hex."`
	Text     string `json:"text" jsonschema:"primary text — must contrast strongly with bg. #rrggbb hex."`
	Muted    string `json:"muted" jsonschema:"secondary text — readable on bg but quieter than text. #rrggbb hex."`
	Accent   string `json:"accent" jsonschema:"buttons, links, active states. #rrggbb hex."`
}

// PaletteView is a palette as returned to the caller. Anchors is a map here
// because that is the persisted shape, and a reader just wants the values.
type PaletteView struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Mode    string            `json:"mode"`
	Builtin bool              `json:"builtin"`
	Anchors map[string]string `json:"anchors"`
}

// AppearanceResult is returned by every theme tool — read and write alike — so
// a model always sees the resulting state and doesn't need a follow-up read to
// know what it just did.
type AppearanceResult struct {
	LightPalette   string        `json:"light_palette"`
	DarkPalette    string        `json:"dark_palette"`
	CustomPalettes []PaletteView `json:"custom_palettes"`
	// Builtins are included so a caller can derive from one ("like Paper but
	// darker") without a second call or hardcoded colour guesses.
	BuiltinPalettes []PaletteView `json:"builtin_palettes"`
}

// UpsertCustomPaletteIn creates a palette, or replaces one wholesale when the
// id already exists. Anchors are required in full on update too: a partial
// patch would leave the caller guessing which roles it had changed.
type UpsertCustomPaletteIn struct {
	ID      string       `json:"id" jsonschema:"stable slug id, e.g. 'ocean-dark'. Reuse an existing custom id to replace that palette; a new id creates one. Cannot be a built-in id."`
	Name    string       `json:"name" jsonschema:"short display name shown in the palette picker"`
	Mode    string       `json:"mode" jsonschema:"light or dark — which mode this palette is selectable in"`
	Anchors ThemeAnchors `json:"anchors" jsonschema:"all 7 colour roles, every one required"`
	// Activating on create is the common intent ("make me a theme and use it"),
	// but it is opt-in so a caller can build a palette without disrupting the
	// user's current look.
	Activate bool `json:"activate,omitempty" jsonschema:"also make this the active palette for its mode. Default false."`
}

// SetActivePaletteIn switches the live palette for one or both modes. Each
// field is optional so a caller can change just the dark theme.
type SetActivePaletteIn struct {
	LightPalette string `json:"light_palette,omitempty" jsonschema:"palette id to use in light mode — a built-in id or one of the caller's custom light palettes. Omit to leave unchanged."`
	DarkPalette  string `json:"dark_palette,omitempty" jsonschema:"palette id to use in dark mode. Omit to leave unchanged."`
}

// DeleteCustomPaletteIn removes one of the caller's own palettes.
type DeleteCustomPaletteIn struct {
	Confirm
	ID string `json:"id" jsonschema:"id of the custom palette to delete. Built-ins cannot be deleted."`
}
