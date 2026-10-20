package mcp_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// While a member holds a segment, the assistant's writes to it as anyone else
// are refused with the holder's name (save_draft, confirm), the batch reports
// it as held, and a non-admin lock is refused. The holder's own tools pass.
func TestIntegrationMCPHold_WritesRefusedWithHolderName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	holder := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "ana@mcphold.test", "pass", "project_manager")
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "ben@mcphold.test", "pass", "project_manager")
	rows, err := f.q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: f.jobA.FileID, TargetLanguage: "de"})
	if err != nil || len(rows) == 0 {
		t.Fatalf("translations: %v", err)
	}
	seeded := "Hallo Welt"
	tr, err := f.q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{ID: rows[0].ID, HumanText: &seeded})
	if err != nil {
		t.Fatal(err)
	}
	testutil.SeedHold(t, f.q, f.tenantA.ID, f.jobA.ID, tr.ID, holder.ID)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	manager := connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID))

	for _, c := range []struct {
		as   string
		tool string
		args map[string]any
	}{
		{"owner", "openloc_save_draft", map[string]any{"translation_id": tr.ID, "text": "Hallo"}},
		{"owner", "openloc_confirm_translation", map[string]any{"translation_id": tr.ID, "text": "Hallo"}},
		{"owner", "openloc_clear_segment", map[string]any{"translation_id": tr.ID}},
		{"manager", "openloc_lock_segment", map[string]any{"translation_id": tr.ID}},
	} {
		s := owner
		if c.as == "manager" {
			s = manager
		}
		if isErr, text := callTool(t, s, c.tool, c.args, nil); !isErr || !strings.Contains(text, holder.Name) {
			t.Errorf("%s as %s: isErr=%v %q; want a refusal naming %s", c.tool, c.as, isErr, text, holder.Name)
		}
	}

	var batch struct {
		Applied []any   `json:"applied"`
		Held    []int64 `json:"held"`
	}
	if isErr, text := callTool(t, owner, "openloc_save_drafts_batch", map[string]any{
		"job_id": f.jobA.ID, "drafts": []map[string]any{{"translation_id": tr.ID, "text": "Hallo"}},
	}, &batch); isErr {
		t.Fatalf("save_drafts_batch: %s", text)
	}
	if len(batch.Applied) != 0 || !reflect.DeepEqual(batch.Held, []int64{tr.ID}) {
		t.Errorf("batch = %+v, want nothing applied and [%d] held", batch, tr.ID)
	}

	got, err := f.q.GetTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveText == nil || *got.ActiveText != seeded || got.Locked || !got.UpdatedAt.Time.Equal(tr.UpdatedAt.Time) {
		t.Fatalf("held row changed: text %v locked %v", got.ActiveText, got.Locked)
	}

	// The holder's own draft passes; an owner's lock is not blocked.
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, holder.ID)),
		"openloc_save_draft", map[string]any{"translation_id": tr.ID, "text": "Hallo"}, nil); isErr {
		t.Errorf("holder's save_draft refused: %s", text)
	}
	if isErr, text := callTool(t, owner, "openloc_lock_segment", map[string]any{"translation_id": tr.ID}, nil); isErr {
		t.Errorf("owner's lock refused: %s", text)
	}
}

// The bulk tools skip a segment another member holds and count it, and write
// the one the caller holds: apply_tm, find_replace_job and
// clear_project_translations report held, reimport_xliff reports
// skipped_held with the key and says so in its preview.
//
// Anti-tautology: dropping the Held copy from a tool's MCP result (e.g.
// ApplyTM's PhaseRunResult) fails that tool's count assertion.
func TestIntegrationMCPHold_BulkToolsSkipAnotherMembersHold(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	ctx := context.Background()
	holder := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "ana@mcphold.test", "pass", "project_manager")
	if _, err := f.pool.Exec(ctx, `UPDATE segments SET segment_key = 'k' || position WHERE file_id = $1`, f.jobA.FileID); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	rows, err := f.pool.Query(ctx, `SELECT s.segment_key, t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'de'`, f.jobA.FileID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			t.Fatal(err)
		}
		ids[key] = id
	}
	if err := rows.Err(); err != nil || len(ids) != 3 {
		t.Fatalf("translations by key: %v %v", ids, err)
	}
	// k0 ("Hello world") is the other member's, k1 ("Second segment") the caller's.
	theirs, mine := ids["k0"], ids["k1"]
	seeded := "Hallo Welt"
	if _, err := f.q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{ID: theirs, HumanText: &seeded}); err != nil {
		t.Fatal(err)
	}
	testutil.SeedHold(t, f.q, f.tenantA.ID, f.jobA.ID, theirs, holder.ID)
	testutil.SeedHold(t, f.q, f.tenantA.ID, f.jobA.ID, mine, f.ownerA.ID)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	text := func(id int64) string { return ptr.Deref(readTranslation(t, f, f.tenantA.ID, id).ActiveText) }
	assertTheirsKept := func(tool string) {
		t.Helper()
		if got := text(theirs); got != seeded {
			t.Fatalf("%s wrote the segment another member holds: %q", tool, got)
		}
	}

	tm := seedWriteTM(t, f)
	for src, tgt := range map[string]string{"Hello world": "Hallo TM", "Second segment": "Hallo zweites Segment"} {
		if _, err := f.q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de", SourceText: src, TargetText: tgt, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
	}
	var run struct {
		Held int `json:"held"`
	}
	if isErr, out := callTool(t, s, "openloc_apply_tm", map[string]any{"job_id": f.jobA.ID}, &run); isErr {
		t.Fatalf("apply_tm: %s", out)
	}
	if run.Held != 1 || text(mine) != "Hallo zweites Segment" {
		t.Errorf("apply_tm: held %d, own segment %q; want 1 and the TM target", run.Held, text(mine))
	}
	assertTheirsKept("apply_tm")

	var fr struct {
		Held     int `json:"held"`
		Segments int `json:"segments"`
	}
	if isErr, out := confirmedCall(t, s, "openloc_find_replace_job",
		map[string]any{"job_id": f.jobA.ID, "find": "Hallo", "replace": "Servus"}, &fr); isErr {
		t.Fatalf("find_replace_job: %s", out)
	}
	if fr.Held != 1 || fr.Segments != 1 || text(mine) != "Servus zweites Segment" {
		t.Errorf("find_replace_job: held %d, segments %d, own segment %q; want 1, 1 and the replaced text", fr.Held, fr.Segments, text(mine))
	}
	assertTheirsKept("find_replace_job")

	xliff := `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file original="alpha.txt" source-language="en" target-language="de" datatype="plaintext">
    <body>
      <trans-unit id="k0"><source>Hello world</source><target>Hallo XLIFF</target></trans-unit>
      <trans-unit id="k1"><source>Second segment</source><target>Zweites XLIFF</target></trans-unit>
    </body>
  </file>
</xliff>`
	reimport := map[string]any{"file_id": f.jobA.FileID, "target_language": "de",
		"upload_id": stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "alpha_de.xlf", []byte(xliff))}
	var g guardedOut
	if isErr, out := callTool(t, s, "openloc_reimport_xliff", reimport, &g); isErr {
		t.Fatalf("reimport preview: %s", out)
	}
	if g.Preview == nil || !slices.Contains(g.Preview.Effects, "1 segment another user has open is skipped.") {
		t.Errorf("reimport preview = %+v, want the held segment's effect line", g.Preview)
	}
	var re struct {
		Updated         int      `json:"updated"`
		SkippedHeld     int      `json:"skipped_held"`
		SkippedHeldKeys []string `json:"skipped_held_keys"`
	}
	if isErr, out := confirmedCall(t, s, "openloc_reimport_xliff", reimport, &re); isErr {
		t.Fatalf("reimport confirm: %s", out)
	}
	if re.SkippedHeld != 1 || !reflect.DeepEqual(re.SkippedHeldKeys, []string{"k0"}) || re.Updated != 1 || text(mine) != "Zweites XLIFF" {
		t.Errorf("reimport = %+v, own segment %q; want k0 held, 1 updated", re, text(mine))
	}
	assertTheirsKept("reimport_xliff")

	var cleared struct {
		Cleared int64 `json:"cleared"`
		Held    int64 `json:"held"`
	}
	if isErr, out := confirmedCall(t, s, "openloc_clear_project_translations",
		map[string]any{"project_id": f.projectA.ID, "scope": "all"}, &cleared); isErr {
		t.Fatalf("clear_project_translations: %s", out)
	}
	if cleared.Held != 1 || text(mine) != "" {
		t.Errorf("clear_project_translations: held %d, own segment %q; want 1 and cleared", cleared.Held, text(mine))
	}
	assertTheirsKept("clear_project_translations")
}
