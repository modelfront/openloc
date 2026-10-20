// job_ops segment tools over the real protocol: confirm (assistant confirms
// stay out of translation memory), batch drafts, comments, history, segment
// clear and QA.
package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type sessionT = *sdk.ClientSession

// seedWriteTM gives project A a read_write TM for en→de.
func seedWriteTM(t *testing.T, f *fixture) db.TmCollection {
	t.Helper()
	tm := testutil.SeedTMWithLangs(t, f.q, f.tenantA.ID, "Job ops TM", "en", []string{"de"})
	if _, err := f.q.CreateSettingsTMAssignment(context.Background(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: f.projectA.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign TM: %v", err)
	}
	return tm
}

func tmEntryCount(t *testing.T, f *fixture, tmID int64) int64 {
	t.Helper()
	n, err := f.q.CountTMEntries(context.Background(), db.CountTMEntriesParams{TmID: tmID})
	if err != nil {
		t.Fatalf("count TM entries: %v", err)
	}
	return n
}

func readTranslation(t *testing.T, f *fixture, tenantID, id int64) db.Translation {
	t.Helper()
	tr, err := f.q.GetTranslationTenant(context.Background(), db.GetTranslationTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		t.Fatalf("read translation %d: %v", id, err)
	}
	return tr
}

// restConfirm confirms a segment through PUT /api/files/{fileID}/translations/{id},
// the editor's Ctrl+Enter.
func restConfirm(t *testing.T, f *fixture, userID, tenantID, fileID, translationID int64, text string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"human_text": text})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/api/files/%d/translations/%d", f.url, fileID, translationID), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(userID, tenantID, "owner"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT confirm: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT confirm: status %d", resp.StatusCode)
	}
}

// The central rule: a segment the assistant confirms is approved but never
// becomes translation memory; a person's later confirm writes it normally.
func TestIntegrationMCPConfirmTranslationSkipsTM(t *testing.T) {
	f := newFixture(t)
	tm := seedWriteTM(t, f)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, s, f.jobA.ID)

	args := map[string]any{"translation_id": translationID, "text": "Hallo Welt"}
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_confirm_translation", args, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Status != "needs_confirmation" || g.Preview == nil {
		t.Fatalf("want a preview, got %+v", g)
	}
	effects := strings.Join(g.Preview.Effects, "\n")
	if !strings.Contains(effects, "Hello world") || !strings.Contains(effects, "Hallo Welt") {
		t.Errorf("preview must show source and the text being confirmed, got %q", effects)
	}
	if got := readTranslation(t, f, f.tenantA.ID, translationID); got.Status != db.TranslationStatusNeedsReview {
		t.Fatalf("the preview changed the segment: status %s", got.Status)
	}

	var res struct {
		Status           string `json:"status"`
		TMEntriesWritten int    `json:"tm_entries_written"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_confirm_translation", args, &res); isErr {
		t.Fatalf("confirm: %s", text)
	}
	got := readTranslation(t, f, f.tenantA.ID, translationID)
	if got.Status != db.TranslationStatusApproved || got.ConfirmedVia != "assistant" {
		t.Fatalf("want approved + confirmed_via=assistant, got %s / %s", got.Status, got.ConfirmedVia)
	}
	if ptr.Deref(got.ActiveText) != "Hallo Welt" {
		t.Errorf("confirmed text = %q", ptr.Deref(got.ActiveText))
	}
	if res.TMEntriesWritten != 0 || tmEntryCount(t, f, tm.ID) != 0 {
		t.Fatalf("an assistant confirm reached translation memory (result %d, TM rows %d)",
			res.TMEntriesWritten, tmEntryCount(t, f, tm.ID))
	}
	// Step/job completion re-lists approved rows for TM write-back; the
	// assistant's confirm must not be among them.
	approved, err := f.q.ListApprovedForTM(context.Background(), db.ListApprovedForTMParams{
		FileID: f.jobA.FileID, TargetLanguage: "de",
	})
	if err != nil {
		t.Fatalf("ListApprovedForTM: %v", err)
	}
	if len(approved) != 0 {
		t.Fatalf("assistant-confirmed row is listed for completion write-back: %+v", approved)
	}

	// A person confirms it afterwards: back to 'user', and TM gets the entry.
	restConfirm(t, f, f.ownerA.ID, f.tenantA.ID, f.jobA.FileID, translationID, "Hallo Welt!")
	got = readTranslation(t, f, f.tenantA.ID, translationID)
	if got.ConfirmedVia != "user" {
		t.Fatalf("a human confirm must reset confirmed_via to user, got %s", got.ConfirmedVia)
	}
	if n := tmEntryCount(t, f, tm.ID); n != 1 {
		t.Fatalf("a human confirm must write TM, got %d entries", n)
	}
}

func TestIntegrationMCPConfirmTranslationRefusals(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, owner, f.jobA.ID)

	// Unassigned linguist: the segment does not exist for them.
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	isErr, text := callTool(t, ling, "openloc_confirm_translation",
		map[string]any{"translation_id": translationID, "text": "x"}, nil)
	if !isErr {
		t.Fatal("an unassigned linguist previewed a confirm")
	}
	assertOpaqueNotFound(t, text)

	// Assigned, the same linguist may.
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	if isErr, text := confirmedCall(t, ling, "openloc_confirm_translation",
		map[string]any{"translation_id": translationID, "text": "Hallo"}, nil); isErr {
		t.Fatalf("assigned linguist confirm: %s", text)
	}

	// Another tenant.
	other := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	isErr, text = callTool(t, other, "openloc_confirm_translation",
		map[string]any{"translation_id": translationID}, nil)
	if !isErr {
		t.Fatal("another tenant previewed a confirm")
	}
	assertOpaqueNotFound(t, text)

	// A locked segment is refused at preview time, like the confirm itself.
	second := secondTranslation(t, owner, f.jobA.ID)
	if err := f.q.LockTranslation(context.Background(), second); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if isErr, text := callTool(t, owner, "openloc_confirm_translation",
		map[string]any{"translation_id": second, "text": "x"}, nil); !isErr || !strings.Contains(text, "locked") {
		t.Errorf("confirming a locked segment should be refused, got isErr=%v %q", isErr, text)
	}
	// Nothing to confirm: no text and an empty segment.
	third := nthTranslation(t, owner, f.jobA.ID, 2)
	if isErr, text := callTool(t, owner, "openloc_confirm_translation",
		map[string]any{"translation_id": third}, nil); !isErr || !strings.Contains(text, "no translation") {
		t.Errorf("confirming an empty segment should be refused, got isErr=%v %q", isErr, text)
	}
}

func nthTranslation(t *testing.T, s sessionT, jobID int64, n int) int64 {
	t.Helper()
	var out struct {
		Segments []struct {
			TranslationID int64 `json:"translation_id"`
		} `json:"segments"`
	}
	if isErr, text := callTool(t, s, "openloc_list_segments", map[string]any{"job_id": jobID}, &out); isErr {
		t.Fatalf("list_segments: %s", text)
	}
	if len(out.Segments) <= n {
		t.Fatalf("want at least %d segments, got %d", n+1, len(out.Segments))
	}
	return out.Segments[n].TranslationID
}

func secondTranslation(t *testing.T, s sessionT, jobID int64) int64 {
	return nthTranslation(t, s, jobID, 1)
}

func TestIntegrationMCPSaveDraftsBatch(t *testing.T) {
	f := newFixture(t)
	tm := seedWriteTM(t, f)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	first, version := firstTranslation(t, s, f.jobA.ID)
	second := secondTranslation(t, s, f.jobA.ID)
	foreign, _ := firstTranslation(t, connect(t, f.url, f.tokenB), f.jobB.ID)

	var out struct {
		Applied []struct {
			TranslationID int64  `json:"translation_id"`
			Status        string `json:"status"`
		} `json:"applied"`
		Conflicts []int64 `json:"conflicts"`
		Rejected  []int64 `json:"rejected"`
	}
	if isErr, text := callTool(t, s, "openloc_save_drafts_batch", map[string]any{
		"job_id": f.jobA.ID,
		"drafts": []map[string]any{
			{"translation_id": first, "text": "Hallo Welt", "expected_updated_at": version},
			{"translation_id": second, "text": "Zweites", "expected_updated_at": "2000-01-01T00:00:00Z"},
			{"translation_id": foreign, "text": "another tenant's segment"},
		},
	}, &out); isErr {
		t.Fatalf("save_drafts_batch: %s", text)
	}
	if len(out.Applied) != 1 || out.Applied[0].TranslationID != first || out.Applied[0].Status != "needs_review" {
		t.Errorf("want the first draft applied as needs_review, got %+v", out.Applied)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0] != second {
		t.Errorf("a stale version must be a conflict, got %v", out.Conflicts)
	}
	if len(out.Rejected) != 1 {
		t.Errorf("another tenant's segment must be rejected, got %v", out.Rejected)
	}
	if ptr.Deref(readTranslation(t, f, f.tenantA.ID, second).ActiveText) != "" {
		t.Error("the conflicting draft overwrote the segment")
	}
	if tmEntryCount(t, f, tm.ID) != 0 {
		t.Error("drafts reached translation memory")
	}

	// Cross-tenant job and read-only token.
	other := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	isErr, text := callTool(t, other, "openloc_save_drafts_batch", map[string]any{
		"job_id": f.jobA.ID, "drafts": []map[string]any{{"translation_id": first, "text": "x"}},
	}, nil)
	if !isErr {
		t.Fatal("another tenant saved drafts")
	}
	assertOpaqueNotFound(t, text)
}

func TestIntegrationMCPCommentsHistoryAndDelete(t *testing.T) {
	f := newFixture(t)
	owner := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, owner, f.jobA.ID)

	var added struct {
		CommentID int64 `json:"comment_id"`
	}
	if isErr, text := callTool(t, owner, "openloc_add_comment",
		map[string]any{"translation_id": translationID, "content": "check the term"}, &added); isErr {
		t.Fatalf("add_comment: %s", text)
	}
	ai, err := f.q.CreateAIComment(context.Background(), db.CreateAICommentParams{TranslationID: translationID, Content: "AI remark"})
	if err != nil {
		t.Fatalf("CreateAIComment: %v", err)
	}

	var list struct {
		Comments []struct {
			CommentID  int64  `json:"comment_id"`
			AuthorName string `json:"author_name"`
			IsAI       bool   `json:"is_ai"`
		} `json:"comments"`
	}
	if isErr, text := callTool(t, owner, "openloc_list_comments", map[string]any{"translation_id": translationID}, &list); isErr {
		t.Fatalf("list_comments: %s", text)
	}
	if len(list.Comments) != 2 || list.Comments[0].IsAI || !list.Comments[1].IsAI {
		t.Fatalf("want the human comment then the AI one, got %+v", list.Comments)
	}

	// A linguist assigned to the job may delete the AI comment but not the owner's.
	testutil.SeedJobStep(t, f.q, f.projectA.SettingsProfileID, f.jobA.ID, &f.linguistA.ID)
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	if isErr, text := callTool(t, ling, "openloc_delete_comment",
		map[string]any{"translation_id": translationID, "comment_id": added.CommentID}, nil); !isErr || !strings.Contains(text, "author") {
		t.Errorf("a linguist must not delete the owner's comment, got isErr=%v %q", isErr, text)
	}
	args := map[string]any{"translation_id": translationID, "comment_id": ai.ID}
	var g guardedOut
	if isErr, text := callTool(t, ling, "openloc_delete_comment", args, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "AI remark") {
		t.Errorf("preview should quote the comment: %+v", g.Preview)
	}
	if isErr, text := confirmedCall(t, ling, "openloc_delete_comment", args, nil); isErr {
		t.Fatalf("delete: %s", text)
	}
	var deletedAt *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM segment_comments WHERE id = $1`, ai.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("read comment row: %v", err)
	}
	if deletedAt == nil {
		t.Error("delete_comment must soft-delete: the row stays with deleted_at")
	}
	list.Comments = nil
	if isErr, text := callTool(t, owner, "openloc_list_comments", map[string]any{"translation_id": translationID}, &list); isErr {
		t.Fatalf("list_comments: %s", text)
	}
	if len(list.Comments) != 1 {
		t.Errorf("a deleted comment is still listed: %+v", list.Comments)
	}

	// History: a confirm through REST shows up with its author.
	restConfirm(t, f, f.ownerA.ID, f.tenantA.ID, f.jobA.FileID, translationID, "Hallo Welt")
	var hist struct {
		Events []struct {
			Kind          string `json:"kind"`
			Text          string `json:"text"`
			CreatedByName string `json:"created_by_name"`
		} `json:"events"`
	}
	if isErr, text := callTool(t, owner, "openloc_get_segment_history", map[string]any{"translation_id": translationID}, &hist); isErr {
		t.Fatalf("history: %s", text)
	}
	if len(hist.Events) == 0 || hist.Events[0].Kind != "human" || hist.Events[0].Text != "Hallo Welt" {
		t.Fatalf("want the confirm as newest history event, got %+v", hist.Events)
	}

	other := connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	for _, tool := range []string{"openloc_list_comments", "openloc_get_segment_history", "openloc_run_segment_qa"} {
		isErr, text := callTool(t, other, tool, map[string]any{"translation_id": translationID}, nil)
		if !isErr {
			t.Errorf("%s: another tenant got through", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPClearSegment(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, version := firstTranslation(t, s, f.jobA.ID)
	if isErr, text := callTool(t, s, "openloc_save_draft",
		map[string]any{"translation_id": translationID, "text": "Hallo", "expected_updated_at": version}, nil); isErr {
		t.Fatalf("save_draft: %s", text)
	}

	args := map[string]any{"translation_id": translationID}
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_clear_segment", args, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "Hallo") {
		t.Errorf("preview should quote the text being cleared: %+v", g.Preview)
	}
	if ptr.Deref(readTranslation(t, f, f.tenantA.ID, translationID).ActiveText) != "Hallo" {
		t.Fatal("the preview cleared the segment")
	}
	if isErr, text := confirmedCall(t, s, "openloc_clear_segment", args, nil); isErr {
		t.Fatalf("clear: %s", text)
	}
	if got := readTranslation(t, f, f.tenantA.ID, translationID); got.ActiveText != nil {
		t.Errorf("segment not cleared: %q", ptr.Deref(got.ActiveText))
	}

	// Nothing left to clear; a read-only token cannot even preview.
	if isErr, _ := callTool(t, s, "openloc_clear_segment", args, nil); !isErr {
		t.Error("clearing an empty segment should be refused")
	}
	read := connect(t, f.url, f.tokenA)
	if isErr, text := callTool(t, read, "openloc_clear_segment", args, nil); !isErr || !strings.Contains(text, "write access") {
		t.Errorf("read-only token previewed a clear: isErr=%v %q", isErr, text)
	}
}

func TestIntegrationMCPRunSegmentQA(t *testing.T) {
	f := newFixture(t)
	attachQAProfile(t, f)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	translationID, _ := firstTranslation(t, s, f.jobA.ID)
	// The source "Hello world" has no number, so the number_consistency rule
	// flags the extra one in the target as number_added.
	target := "Hallo Welt 42"
	if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: translationID, HumanText: &target}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if rows, err := f.q.ListQAResultsForTranslation(ctx, translationID); err != nil || len(rows) != 0 {
		t.Fatalf("want no stored findings before the run, got %+v (err %v)", rows, err)
	}

	var out struct {
		TranslationID int64        `json:"translation_id"`
		Findings      []mcpFinding `json:"findings"`
	}
	if isErr, text := callTool(t, s, "openloc_run_segment_qa", map[string]any{"translation_id": translationID}, &out); isErr {
		t.Fatalf("run_segment_qa: %s", text)
	}
	if out.TranslationID != translationID {
		t.Errorf("want findings for %d, got %d", translationID, out.TranslationID)
	}
	if fnd := findMCP(out.Findings, "number_added"); fnd == nil || fnd.Severity != "warning" {
		t.Fatalf("want a number_added warning, got %+v", out.Findings)
	}
}
