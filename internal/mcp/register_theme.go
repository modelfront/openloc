package mcp

import sdk "github.com/modelcontextprotocol/go-sdk/mcp"

// registerTheme registers the theme tools.
//
// Deliberately NOT manager-gated: a palette is a personal preference stored
// per user, so a linguist restyling their own UI needs no elevated role.
// They are still requireWrite, so a read-only token can't change them and
// the chat asks for confirmation first.
func registerTheme(s *Server, srv *sdk.Server, managerTier bool, b Backend) {
	addTool(s, srv, managerTier, toolSpec{
		name:  toolGetAppearance,
		title: "Get appearance settings",
		description: "Read the caller's own theme: which palette is active in light and dark " +
			"mode, their custom palettes, and every built-in palette with its colours. " +
			"Call this FIRST when asked to change or derive a theme — the built-in " +
			"anchors let you build on an existing look instead of guessing colours.",
	}, b.GetAppearance)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolUpsertPalette,
		title: "Save custom color palette",
		description: "Create or replace one of the caller's own custom palettes. Give all 7 " +
			"colour roles as #rrggbb hex; reusing an existing custom id replaces that " +
			"palette wholesale. Make text and muted clearly readable against bg, and " +
			"keep a dark palette's bg dark. Set activate=true to switch to it " +
			"immediately, which is usually what someone asking for a new theme wants.",
		requireWrite: true,
	}, b.UpsertCustomPalette)

	addTool(s, srv, managerTier, toolSpec{
		name:  toolSetActivePalette,
		title: "Set active color palette",
		description: "Switch which palette is live for the caller, per mode. Pass light_palette, " +
			"dark_palette, or both; omit a mode to leave it alone. The id must be a " +
			"built-in or one of the caller's own palettes for that same mode.",
		requireWrite: true,
	}, b.SetActivePalette)

	addGuardedTool(s, srv, managerTier, toolSpec{
		name:  toolDeleteCustomPalette,
		title: "Delete custom color palette",
		description: "Delete one of the caller's custom palettes. Built-ins cannot be deleted. " +
			"If the palette is currently live, the mode falls back to its default. " +
			"The colours are not recoverable. Two calls: the first returns a preview, " +
			"the second (with confirm_token, after the user approves) deletes.",
		destructive: true,
	}, b.PreviewDeleteCustomPalette, b.DeleteCustomPalette)
}
