package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// guardedOut is the wire shape of every guarded tool's result (guard.go).
type guardedOut struct {
	Status       string `json:"status"`
	ConfirmToken string `json:"confirm_token"`
	Preview      *struct {
		Summary string   `json:"summary"`
		Effects []string `json:"effects"`
	} `json:"preview"`
	Result json.RawMessage `json:"result"`
}

// confirmedCall runs a guarded tool's preview, then confirms it with the
// returned token, decoding the final result into out. Use it where a test is
// about the tool's effect, not about the guard.
func confirmedCall(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any, out any) (isErr bool, text string) {
	t.Helper()
	return confirmedCallOn(t, s, s, tool, args, out)
}

// confirmedCallOn is confirmedCall with the preview and the confirmation sent
// on different sessions, e.g. a confirmation on a server whose database fails.
func confirmedCallOn(t *testing.T, preview, confirm *sdk.ClientSession, tool string, args map[string]any, out any) (isErr bool, text string) {
	t.Helper()
	var g guardedOut
	if isErr, text := callTool(t, preview, tool, args, &g); isErr {
		return true, text
	}
	if g.Status != "needs_confirmation" || g.ConfirmToken == "" {
		t.Fatalf("%s: first call should return a preview, got %+v", tool, g)
	}
	confirmed := map[string]any{"confirm_token": g.ConfirmToken}
	for k, v := range args {
		confirmed[k] = v
	}
	g = guardedOut{}
	if isErr, text := callTool(t, confirm, tool, confirmed, &g); isErr {
		return true, text
	}
	if g.Status != "done" {
		t.Fatalf("%s: confirmed call should be done, got %+v", tool, g)
	}
	if out != nil {
		if err := json.Unmarshal(g.Result, out); err != nil {
			t.Fatalf("%s: decode result: %v", tool, err)
		}
	}
	return false, ""
}

func TestIntegrationMCPGuardedToolPreviewThenConfirm(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	for _, id := range []string{"ocean", "forest"} {
		if isErr, text := callTool(t, s, "openloc_upsert_custom_palette", map[string]any{
			"id": id, "name": strings.ToUpper(id[:1]) + id[1:], "mode": "dark", "anchors": darkAnchors(),
		}, nil); isErr {
			t.Fatalf("upsert %s: %s", id, text)
		}
	}
	customsOf := func(sess *sdk.ClientSession) []string {
		var out appearanceOut
		if isErr, text := callTool(t, sess, "openloc_get_appearance", map[string]any{}, &out); isErr {
			t.Fatalf("get_appearance: %s", text)
		}
		ids := []string{}
		for _, p := range out.CustomPalettes {
			ids = append(ids, p.ID)
		}
		return ids
	}
	customs := func() []string { return customsOf(s) }

	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_delete_custom_palette", map[string]any{"id": "ocean"}, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Status != "needs_confirmation" || g.ConfirmToken == "" || g.Preview == nil {
		t.Fatalf("want a preview with a token, got %+v", g)
	}
	if !strings.Contains(g.Preview.Summary, "Ocean") {
		t.Errorf("preview should name the palette: %q", g.Preview.Summary)
	}
	if got := customs(); len(got) != 2 {
		t.Fatalf("the preview deleted something: %v", got)
	}

	// The token is bound to the arguments: it cannot delete a different palette.
	if isErr, text := callTool(t, s, "openloc_delete_custom_palette",
		map[string]any{"id": "forest", "confirm_token": g.ConfirmToken}, nil); !isErr {
		t.Error("a token for ocean deleted forest")
	} else if !strings.Contains(text, "fresh preview") {
		t.Errorf("refusal should explain how to recover: %q", text)
	}
	if isErr, _ := callTool(t, s, "openloc_delete_custom_palette",
		map[string]any{"id": "ocean", "confirm_token": "123.forged"}, nil); !isErr {
		t.Error("a forged token was accepted")
	}
	if got := customs(); len(got) != 2 {
		t.Fatalf("a refused confirm deleted something: %v", got)
	}

	// Another user in the same tenant cannot use the owner's token, even on a
	// palette of their own with the same id: the token is bound to the user.
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	if isErr, text := callTool(t, ling, "openloc_upsert_custom_palette", map[string]any{
		"id": "ocean", "name": "Ocean", "mode": "dark", "anchors": darkAnchors(),
	}, nil); isErr {
		t.Fatalf("linguist upsert ocean: %s", text)
	}
	if isErr, text := callTool(t, ling, "openloc_delete_custom_palette",
		map[string]any{"id": "ocean", "confirm_token": g.ConfirmToken}, nil); !isErr {
		t.Error("another user's token was accepted")
	} else if !strings.Contains(text, "fresh preview") {
		t.Errorf("want the token refusal, got %q", text)
	}
	if got := customsOf(ling); len(got) != 1 || got[0] != "ocean" {
		t.Errorf("the linguist's palettes after a refused confirm = %v, want [ocean]", got)
	}
	if got := customs(); len(got) != 2 {
		t.Fatalf("a refused confirm deleted one of the owner's palettes: %v", got)
	}

	var done guardedOut
	if isErr, text := callTool(t, s, "openloc_delete_custom_palette",
		map[string]any{"id": "ocean", "confirm_token": g.ConfirmToken}, &done); isErr {
		t.Fatalf("confirm: %s", text)
	}
	if done.Status != "done" || len(done.Result) == 0 {
		t.Errorf("want done with a result, got %+v", done)
	}
	if got := customs(); len(got) != 1 || got[0] != "forest" {
		t.Errorf("after confirm want only forest, got %v", got)
	}
}
