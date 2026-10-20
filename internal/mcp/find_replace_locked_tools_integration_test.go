package mcp_test

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// lockFirstOfJobA gives every translation of job A the text "Hallo Welt" and
// locks the first one; it returns the locked translation's id.
func lockFirstOfJobA(t *testing.T, f *fixture) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx,
		`UPDATE translations t SET active_text = 'Hallo Welt' FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1`,
		f.jobA.FileID); err != nil {
		t.Fatalf("set text: %v", err)
	}
	var id int64
	if err := f.pool.QueryRow(ctx,
		`SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id WHERE s.file_id = $1 ORDER BY s.position LIMIT 1`,
		f.jobA.FileID).Scan(&id); err != nil {
		t.Fatalf("pick translation: %v", err)
	}
	if err := f.q.LockTranslation(ctx, id); err != nil {
		t.Fatalf("lock: %v", err)
	}
	return id
}

// The assistant's find and replace follows the editor: locked segments are
// left out unless include_locked is set, and then written only for an owner or
// admin. The preview says which of the two happens.
func TestIntegrationMCPFindReplaceIncludeLocked(t *testing.T) {
	type frOut struct {
		Replaced int `json:"replaced"`
		Segments int `json:"segments"`
		Locked   int `json:"locked"`
	}

	t.Run("left out by default", func(t *testing.T) {
		f := newFixture(t)
		locked := lockFirstOfJobA(t, f)
		s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
		fr := map[string]any{"job_id": f.jobA.ID, "find": "Welt", "replace": "Erde"}

		var g guardedOut
		if isErr, text := callTool(t, s, "openloc_find_replace_job", fr, &g); isErr {
			t.Fatalf("preview: %s", text)
		}
		if g.Preview == nil || !strings.Contains(g.Preview.Summary, "in 2 segment(s)") || strings.Contains(strings.Join(g.Preview.Effects, "\n"), "locked") {
			t.Fatalf("preview must count only the 2 unlocked segments and not mention locked ones: %+v", g.Preview)
		}
		var out frOut
		if isErr, text := confirmedCall(t, s, "openloc_find_replace_job", fr, &out); isErr {
			t.Fatalf("replace: %s", text)
		}
		if out.Segments != 2 || out.Locked != 0 {
			t.Errorf("result = %+v, want 2 segments and 0 locked", out)
		}
		if got := ptr.Deref(readTranslation(t, f, f.tenantA.ID, locked).ActiveText); got != "Hallo Welt" {
			t.Errorf("locked segment = %q, want it unchanged", got)
		}
	})

	t.Run("written for an owner with include_locked", func(t *testing.T) {
		f := newFixture(t)
		locked := lockFirstOfJobA(t, f)
		s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
		fr := map[string]any{"job_id": f.jobA.ID, "find": "Welt", "replace": "Erde", "include_locked": true}

		var g guardedOut
		if isErr, text := callTool(t, s, "openloc_find_replace_job", fr, &g); isErr {
			t.Fatalf("preview: %s", text)
		}
		if g.Preview == nil || !strings.Contains(strings.Join(g.Preview.Effects, "\n"), "1 locked segment(s) match and will be overwritten") {
			t.Fatalf("preview must say the locked segment will be overwritten: %+v", g.Preview)
		}
		var out frOut
		if isErr, text := confirmedCall(t, s, "openloc_find_replace_job", fr, &out); isErr {
			t.Fatalf("replace: %s", text)
		}
		got := readTranslation(t, f, f.tenantA.ID, locked)
		if out.Segments != 3 || ptr.Deref(got.ActiveText) != "Hallo Erde" || got.Locked {
			t.Errorf("result = %+v, locked segment = %q (locked=%v); want 3 segments and the locked one replaced and unlocked", out, ptr.Deref(got.ActiveText), got.Locked)
		}
	})

	t.Run("left alone for a project manager with include_locked", func(t *testing.T) {
		f := newFixture(t)
		locked := lockFirstOfJobA(t, f)
		pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "mcp-pm@test.com", "pass", "project_manager")
		s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))
		fr := map[string]any{"job_id": f.jobA.ID, "find": "Welt", "replace": "Erde", "include_locked": true}

		var g guardedOut
		if isErr, text := callTool(t, s, "openloc_find_replace_job", fr, &g); isErr {
			t.Fatalf("preview: %s", text)
		}
		if g.Preview == nil || !strings.Contains(strings.Join(g.Preview.Effects, "\n"), "1 locked segment(s) match and will be left as they are") {
			t.Fatalf("preview must say the locked segment stays: %+v", g.Preview)
		}
		var out frOut
		if isErr, text := confirmedCall(t, s, "openloc_find_replace_job", fr, &out); isErr {
			t.Fatalf("replace: %s", text)
		}
		if out.Segments != 2 || out.Locked != 1 {
			t.Errorf("result = %+v, want 2 segments and 1 locked", out)
		}
		if got := ptr.Deref(readTranslation(t, f, f.tenantA.ID, locked).ActiveText); got != "Hallo Welt" {
			t.Errorf("locked segment = %q, want it unchanged", got)
		}
	})
}
