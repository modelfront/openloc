// Theme tools over the real protocol: get_appearance,
// upsert_custom_palette, set_active_palette, delete_custom_palette.
//
// These are the only USER-scoped tools in the package, so the isolation test
// here checks a different axis from the rest of the suite: not "can tenant A
// see tenant B", but "can user A see user B" — two users in the SAME tenant
// must not see each other's palettes.
package mcp_test

import (
	"strings"
	"testing"
)

type paletteView struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Mode    string            `json:"mode"`
	Builtin bool              `json:"builtin"`
	Anchors map[string]string `json:"anchors"`
}

type appearanceOut struct {
	LightPalette    string        `json:"light_palette"`
	DarkPalette     string        `json:"dark_palette"`
	CustomPalettes  []paletteView `json:"custom_palettes"`
	BuiltinPalettes []paletteView `json:"builtin_palettes"`
}

// darkAnchors is a valid 7-role dark palette.
func darkAnchors() map[string]any {
	return map[string]any{
		"bg": "#0b1020", "surface": "#121a33", "elevated": "#1b2547", "border": "#2a3660",
		"text": "#eef2ff", "muted": "#93a1c9", "accent": "#5b8cff",
	}
}

func TestIntegrationMCPGetAppearanceDefaults(t *testing.T) {
	f := newFixture(t)
	// A read token is enough: get_appearance is not a write.
	s := connect(t, f.url, f.tokenA)

	var out appearanceOut
	if isErr, text := callTool(t, s, "openloc_get_appearance", map[string]any{}, &out); isErr {
		t.Fatalf("get_appearance failed: %s", text)
	}
	// Brand-new user has no row at all; that is a default, not an error.
	if out.LightPalette != "paper" || out.DarkPalette != "dim" {
		t.Errorf("defaults wrong: light=%q dark=%q", out.LightPalette, out.DarkPalette)
	}
	if len(out.CustomPalettes) != 0 {
		t.Errorf("fresh user has customs: %+v", out.CustomPalettes)
	}
	// Built-ins are the reason this tool exists in read form — without their
	// anchors a model has to invent colours instead of deriving from one.
	if len(out.BuiltinPalettes) != 6 {
		t.Fatalf("want 6 builtins, got %d", len(out.BuiltinPalettes))
	}
	var paper *paletteView
	for i := range out.BuiltinPalettes {
		if out.BuiltinPalettes[i].ID == "paper" {
			paper = &out.BuiltinPalettes[i]
		}
	}
	if paper == nil {
		t.Fatal("paper missing from builtins")
	}
	if paper.Anchors["bg"] != "#faf9f7" || len(paper.Anchors) != 7 {
		t.Errorf("paper anchors not exposed in full: %+v", paper.Anchors)
	}
	if !paper.Builtin {
		t.Error("builtin flag not set on a built-in palette")
	}
}

func TestIntegrationMCPUpsertAndActivatePalette(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out appearanceOut
	if isErr, text := callTool(t, s, "openloc_upsert_custom_palette", map[string]any{
		"id": "midnight", "name": "Midnight", "mode": "dark",
		"anchors": darkAnchors(), "activate": true,
	}, &out); isErr {
		t.Fatalf("upsert failed: %s", text)
	}
	if len(out.CustomPalettes) != 1 || out.CustomPalettes[0].ID != "midnight" {
		t.Fatalf("palette not stored: %+v", out.CustomPalettes)
	}
	if out.CustomPalettes[0].Builtin {
		t.Error("a custom palette must never be flagged builtin — that would make it undeletable")
	}
	// activate=true applies to the palette's OWN mode and leaves the other one
	// alone, so asking for a dark theme doesn't change light.
	if out.DarkPalette != "midnight" {
		t.Errorf("activate did not switch dark: %q", out.DarkPalette)
	}
	if out.LightPalette != "paper" {
		t.Errorf("activate touched the other mode: light=%q", out.LightPalette)
	}

	// Same id again = wholesale replace, not a second entry.
	anchors := darkAnchors()
	anchors["accent"] = "#ff0088"
	if isErr, text := callTool(t, s, "openloc_upsert_custom_palette", map[string]any{
		"id": "midnight", "name": "Midnight II", "mode": "dark", "anchors": anchors,
	}, &out); isErr {
		t.Fatalf("replace failed: %s", text)
	}
	if len(out.CustomPalettes) != 1 {
		t.Fatalf("replace created a duplicate: %+v", out.CustomPalettes)
	}
	if out.CustomPalettes[0].Name != "Midnight II" || out.CustomPalettes[0].Anchors["accent"] != "#ff0088" {
		t.Errorf("replace did not overwrite: %+v", out.CustomPalettes[0])
	}
}

func TestIntegrationMCPPaletteValidation(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	badHex := darkAnchors()
	badHex["bg"] = "navy" // not #rrggbb

	missing := darkAnchors()
	delete(missing, "muted")

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"bad hex", map[string]any{
			"id": "x", "name": "X", "mode": "dark", "anchors": badHex}, "hex"},
		{"bad mode", map[string]any{
			"id": "x", "name": "X", "mode": "purple", "anchors": darkAnchors()}, "light or dark"},
		// Caught by the JSON SCHEMA, not by appearance.ValidatePalette: ThemeAnchors
		// is a struct with 7 required fields, so the protocol rejects this before
		// any Go validation runs — earlier and more precise for a model than a
		// generic "missing colors". The Go check still guards the REST path, where
		// anchors arrive as a free-form map.
		{"missing anchor", map[string]any{
			"id": "x", "name": "X", "mode": "dark", "anchors": missing}, "muted"},
		{"empty name", map[string]any{
			"id": "x", "name": "  ", "mode": "dark", "anchors": darkAnchors()}, "id and a name"},
		// Shadowing a built-in id would make the built-in unreachable in the
		// picker and confuse resolution on both sides.
		{"builtin id", map[string]any{
			"id": "dim", "name": "Mine", "mode": "dark", "anchors": darkAnchors()}, "built-in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out appearanceOut
			isErr, text := callTool(t, s, "openloc_upsert_custom_palette", tc.args, &out)
			if !isErr {
				t.Fatalf("expected rejection, got %+v", out)
			}
			if !strings.Contains(strings.ToLower(text), strings.ToLower(tc.want)) {
				t.Errorf("error %q does not mention %q", text, tc.want)
			}
		})
	}

	// Nothing was persisted by any of the rejected calls.
	var out appearanceOut
	if isErr, text := callTool(t, s, "openloc_get_appearance", map[string]any{}, &out); isErr {
		t.Fatalf("get_appearance failed: %s", text)
	}
	if len(out.CustomPalettes) != 0 {
		t.Errorf("a rejected upsert still wrote something: %+v", out.CustomPalettes)
	}
}

func TestIntegrationMCPSetActivePalette(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out appearanceOut
	// Switching to a built-in needs no custom palette at all.
	if isErr, text := callTool(t, s, "openloc_set_active_palette", map[string]any{
		"light_palette": "mist",
	}, &out); isErr {
		t.Fatalf("set light failed: %s", text)
	}
	if out.LightPalette != "mist" || out.DarkPalette != "dim" {
		t.Errorf("omitted mode should be untouched: %+v", out)
	}

	// An id that resolves to nothing must fail loudly rather than silently
	// falling back to the default the way the UI would.
	if isErr, text := callTool(t, s, "openloc_set_active_palette", map[string]any{
		"dark_palette": "nope",
	}, &out); !isErr {
		t.Error("unknown palette id was accepted")
	} else if !strings.Contains(text, "nope") {
		t.Errorf("error should name the missing id: %q", text)
	}

	// Mode mismatch: a light palette cannot be the dark one.
	if isErr, text := callTool(t, s, "openloc_set_active_palette", map[string]any{
		"dark_palette": "paper",
	}, &out); !isErr {
		t.Error("light palette accepted as the dark one")
	} else if !strings.Contains(text, "light") {
		t.Errorf("error should explain the mode clash: %q", text)
	}

	// Neither field given is a caller mistake worth naming.
	if isErr, _ := callTool(t, s, "openloc_set_active_palette", map[string]any{}, &out); !isErr {
		t.Error("empty set_active_palette was accepted")
	}
}

func TestIntegrationMCPDeleteCustomPalette(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var out appearanceOut
	if isErr, text := callTool(t, s, "openloc_upsert_custom_palette", map[string]any{
		"id": "doomed", "name": "Doomed", "mode": "dark",
		"anchors": darkAnchors(), "activate": true,
	}, &out); isErr {
		t.Fatalf("upsert failed: %s", text)
	}
	if out.DarkPalette != "doomed" {
		t.Fatalf("precondition: palette should be active, got %q", out.DarkPalette)
	}

	if isErr, text := confirmedCall(t, s, "openloc_delete_custom_palette", map[string]any{
		"id": "doomed",
	}, &out); isErr {
		t.Fatalf("delete failed: %s", text)
	}
	if len(out.CustomPalettes) != 0 {
		t.Errorf("palette survived delete: %+v", out.CustomPalettes)
	}
	// Deleting the LIVE palette must not leave the row pointing at a dead id —
	// the user would get the fallback anyway, so store it explicitly.
	if out.DarkPalette != "dim" {
		t.Errorf("active palette not reset to the default after deleting it: %q", out.DarkPalette)
	}

	// Built-ins are not deletable.
	if isErr, text := callTool(t, s, "openloc_delete_custom_palette", map[string]any{
		"id": "paper",
	}, &out); !isErr {
		t.Error("built-in palette was deleted")
	} else if !strings.Contains(text, "built-in") {
		t.Errorf("error should say it is built-in: %q", text)
	}

	// Deleting something absent names the id — it's the caller's own data, so
	// there is nothing to hide behind a generic not-found.
	if isErr, text := callTool(t, s, "openloc_delete_custom_palette", map[string]any{
		"id": "ghost",
	}, &out); !isErr {
		t.Error("deleting a missing palette succeeded")
	} else if !strings.Contains(text, "ghost") {
		t.Errorf("error should name the id: %q", text)
	}
}

// TestIntegrationMCPThemeIsPerUser is the isolation test for this tool family.
// The table is keyed by user_id, NOT tenant_id, so the boundary that
// matters is between two users — and here they are in the SAME tenant, which is
// the case a tenant-scoped check would miss entirely.
func TestIntegrationMCPThemeIsPerUser(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	linguist := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))

	var out appearanceOut
	if isErr, text := callTool(t, owner, "openloc_upsert_custom_palette", map[string]any{
		"id": "owners-own", "name": "Owner's own", "mode": "dark",
		"anchors": darkAnchors(), "activate": true,
	}, &out); isErr {
		t.Fatalf("owner upsert failed: %s", text)
	}

	// The linguist — same tenant — sees their own untouched defaults.
	var ling appearanceOut
	if isErr, text := callTool(t, linguist, "openloc_get_appearance", map[string]any{}, &ling); isErr {
		t.Fatalf("linguist get_appearance failed: %s", text)
	}
	if len(ling.CustomPalettes) != 0 {
		t.Errorf("linguist can see another user's palettes: %+v", ling.CustomPalettes)
	}
	if ling.DarkPalette != "dim" {
		t.Errorf("another user's activation leaked: dark=%q", ling.DarkPalette)
	}

	// And the linguist cannot delete the owner's palette even by guessing its
	// id — there is no id parameter that crosses users.
	if isErr, _ := callTool(t, linguist, "openloc_delete_custom_palette", map[string]any{
		"id": "owners-own",
	}, &ling); !isErr {
		t.Error("linguist deleted another user's palette")
	}

	var still appearanceOut
	if isErr, _ := callTool(t, owner, "openloc_get_appearance", map[string]any{}, &still); isErr {
		t.Fatal("owner get_appearance failed")
	}
	if len(still.CustomPalettes) != 1 {
		t.Errorf("owner's palette was affected by another user: %+v", still.CustomPalettes)
	}
}

// A read-capability token must not be able to restyle anything: the writes are
// gated by requireWrite even though they are not manager-gated.
func TestIntegrationMCPThemeWritesNeedWriteToken(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA) // read-only

	var out appearanceOut
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"openloc_upsert_custom_palette", map[string]any{
			"id": "x", "name": "X", "mode": "dark", "anchors": darkAnchors()}},
		{"openloc_set_active_palette", map[string]any{"dark_palette": "graphite"}},
		{"openloc_delete_custom_palette", map[string]any{"id": "x"}},
	} {
		isErr, text := callTool(t, s, tc.tool, tc.args, &out)
		if !isErr {
			t.Errorf("%s succeeded with a read-only token", tc.tool)
			continue
		}
		if !strings.Contains(text, "write") {
			t.Errorf("%s: error should explain the write gate, got %q", tc.tool, text)
		}
	}
}
