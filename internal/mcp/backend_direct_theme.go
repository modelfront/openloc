package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"openloc/internal/appearance"
	db "openloc/internal/database/sqlcgen"
)

// Theme tools.
//
// USER-scoped, unlike every other tool in this package. user_appearance is
// keyed by user_id by design so a palette follows the person across
// orgs. The id comes from the token's claims via scope(), never from the
// payload, so a caller can only read and write their own theme — there is no
// parameter to tamper with and nothing to cross-tenant leak.
//
// Validation is appearance.ValidateCustomPalettes, the same function the REST
// handler calls. Duplicating those rules here would let the two paths drift on
// exactly the thing (hex format, anchor completeness) that makes a palette
// renderable.

// loadAppearance reads the caller's row, falling back to built-in defaults when
// they have none yet — a brand-new user has no row, which is not an error.
func (b *directBackend) loadAppearance(ctx context.Context, userID int64) (string, string, []appearance.Palette, error) {
	a, err := b.q.GetUserAppearance(ctx, userID)
	if err != nil {
		// No row yet. GetUserAppearance returns ErrNoRows here; treating any
		// error as "defaults" matches the REST handler's behaviour rather than
		// failing a read the UI treats as always-succeeding.
		return appearance.DefaultLightPalette, appearance.DefaultDarkPalette, []appearance.Palette{}, nil
	}
	customs := []appearance.Palette{}
	if len(a.CustomPalettes) > 0 {
		if err := json.Unmarshal(a.CustomPalettes, &customs); err != nil {
			return "", "", nil, fmt.Errorf("decode custom palettes: %w", err)
		}
	}
	return a.LightPalette, a.DarkPalette, customs, nil
}

// saveAppearance validates and persists the whole appearance row.
func (b *directBackend) saveAppearance(
	ctx context.Context, userID int64, light, dark string, customs []appearance.Palette,
) (AppearanceResult, error) {
	if err := appearance.ValidateCustomPalettes(customs); err != nil {
		return AppearanceResult{}, invalidf("%s", err.Error())
	}
	// An active id that resolves to nothing would leave the UI silently falling
	// back to the mode default. Better to fail model-visibly than to "succeed"
	// into a theme the user never sees.
	if err := assertResolvable(light, "light", customs); err != nil {
		return AppearanceResult{}, err
	}
	if err := assertResolvable(dark, "dark", customs); err != nil {
		return AppearanceResult{}, err
	}

	raw, err := json.Marshal(customs)
	if err != nil {
		return AppearanceResult{}, fmt.Errorf("encode custom palettes: %w", err)
	}
	saved, err := b.q.UpsertUserAppearance(ctx, db.UpsertUserAppearanceParams{
		UserID:         userID,
		LightPalette:   light,
		DarkPalette:    dark,
		CustomPalettes: raw,
	})
	if err != nil {
		return AppearanceResult{}, fmt.Errorf("save appearance: %w", err)
	}
	return buildAppearanceResult(saved.LightPalette, saved.DarkPalette, customs), nil
}

// assertResolvable checks an active palette id exists for that mode, as either
// a built-in or one of the caller's customs.
func assertResolvable(id, mode string, customs []appearance.Palette) error {
	if p, ok := appearance.BuiltinByID(id); ok {
		if p.Mode != mode {
			return invalidf("%q is a %s palette, so it cannot be the %s one", id, p.Mode, mode)
		}
		return nil
	}
	for _, c := range customs {
		if c.ID == id {
			if c.Mode != mode {
				return invalidf("%q is a %s palette, so it cannot be the %s one", id, c.Mode, mode)
			}
			return nil
		}
	}
	return invalidf("no palette %q — create it first, or pick a built-in", id)
}

func toView(p appearance.Palette, builtin bool) PaletteView {
	return PaletteView{
		ID: p.ID, Name: p.Name, Mode: p.Mode, Builtin: builtin, Anchors: p.Anchors,
	}
}

func buildAppearanceResult(light, dark string, customs []appearance.Palette) AppearanceResult {
	res := AppearanceResult{
		LightPalette:    light,
		DarkPalette:     dark,
		CustomPalettes:  make([]PaletteView, 0, len(customs)),
		BuiltinPalettes: make([]PaletteView, 0, len(appearance.BuiltinPalettes)),
	}
	for _, c := range customs {
		res.CustomPalettes = append(res.CustomPalettes, toView(c, false))
	}
	for _, p := range appearance.BuiltinPalettes {
		res.BuiltinPalettes = append(res.BuiltinPalettes, toView(p, true))
	}
	return res
}

// toAnchors converts the tool's named-field input to the persisted map. The
// named fields exist so the schema tells a model what each colour paints;
// storage stays a map because that is the JSONB shape and what the validator
// checks for completeness.
func (a ThemeAnchors) toAnchors() map[string]string {
	return map[string]string{
		"bg": a.Bg, "surface": a.Surface, "elevated": a.Elevated, "border": a.Border,
		"text": a.Text, "muted": a.Muted, "accent": a.Accent,
	}
}

func (b *directBackend) GetAppearance(ctx context.Context, _ GetAppearanceIn) (AppearanceResult, error) {
	_, userID := scope(ctx)
	light, dark, customs, err := b.loadAppearance(ctx, userID)
	if err != nil {
		return AppearanceResult{}, err
	}
	return buildAppearanceResult(light, dark, customs), nil
}

func (b *directBackend) UpsertCustomPalette(ctx context.Context, in UpsertCustomPaletteIn) (AppearanceResult, error) {
	_, userID := scope(ctx)
	light, dark, customs, err := b.loadAppearance(ctx, userID)
	if err != nil {
		return AppearanceResult{}, err
	}

	next := appearance.Palette{
		ID: in.ID, Name: in.Name, Mode: in.Mode, Anchors: in.Anchors.toAnchors(),
	}
	// Validate the single palette before touching the set, so the error names
	// the palette the caller sent rather than something already stored.
	if err := appearance.ValidatePalette(&next); err != nil {
		return AppearanceResult{}, invalidf("%s", err.Error())
	}

	replaced := false
	for i := range customs {
		if customs[i].ID == next.ID {
			// Wholesale replace, not merge: a partial update would leave the
			// caller unsure which roles it had actually changed.
			customs[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		customs = append(customs, next)
	}

	if in.Activate {
		if next.Mode == "dark" {
			dark = next.ID
		} else {
			light = next.ID
		}
	}
	return b.saveAppearance(ctx, userID, light, dark, customs)
}

func (b *directBackend) SetActivePalette(ctx context.Context, in SetActivePaletteIn) (AppearanceResult, error) {
	_, userID := scope(ctx)
	light, dark, customs, err := b.loadAppearance(ctx, userID)
	if err != nil {
		return AppearanceResult{}, err
	}
	if in.LightPalette == "" && in.DarkPalette == "" {
		return AppearanceResult{}, invalidf("give light_palette, dark_palette, or both")
	}
	if in.LightPalette != "" {
		light = in.LightPalette
	}
	if in.DarkPalette != "" {
		dark = in.DarkPalette
	}
	return b.saveAppearance(ctx, userID, light, dark, customs)
}

// paletteDeletion loads the caller's appearance and splits off the palette
// to delete. Shared by the preview and the delete so they refuse the same ids.
func (b *directBackend) paletteDeletion(ctx context.Context, id string) (
	light, dark string, doomed appearance.Palette, kept []appearance.Palette, err error,
) {
	_, userID := scope(ctx)
	light, dark, customs, err := b.loadAppearance(ctx, userID)
	if err != nil {
		return "", "", appearance.Palette{}, nil, err
	}
	if _, isBuiltin := appearance.BuiltinByID(id); isBuiltin {
		return "", "", appearance.Palette{}, nil, invalidf("%q is a built-in palette and cannot be deleted", id)
	}
	kept = make([]appearance.Palette, 0, len(customs))
	found := false
	for _, c := range customs {
		if c.ID == id {
			doomed, found = c, true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		// Not errNotFound: this is the caller's own data, so naming the missing
		// id leaks nothing and is far more useful than a bare "not found".
		return "", "", appearance.Palette{}, nil, invalidf("no custom palette %q", id)
	}
	return light, dark, doomed, kept, nil
}

func (b *directBackend) PreviewDeleteCustomPalette(ctx context.Context, in DeleteCustomPaletteIn) (Preview, error) {
	light, dark, doomed, _, err := b.paletteDeletion(ctx, in.ID)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Summary: fmt.Sprintf("Delete your custom palette %q. The colours cannot be recovered.", doomed.Name)}
	if light == in.ID || dark == in.ID {
		p.Effects = append(p.Effects, "It is your active palette; the default palette takes its place.")
	}
	return p, nil
}

func (b *directBackend) DeleteCustomPalette(ctx context.Context, in DeleteCustomPaletteIn) (AppearanceResult, error) {
	_, userID := scope(ctx)
	light, dark, _, kept, err := b.paletteDeletion(ctx, in.ID)
	if err != nil {
		return AppearanceResult{}, err
	}

	// Deleting the palette that is currently live would leave the row pointing
	// at an id that no longer exists. Fall back to the mode's default so the
	// user always has a valid theme.
	if light == in.ID {
		light = appearance.DefaultLightPalette
	}
	if dark == in.ID {
		dark = appearance.DefaultDarkPalette
	}
	return b.saveAppearance(ctx, userID, light, dark, kept)
}
