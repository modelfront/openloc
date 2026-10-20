package pipeline

import (
	"bytes"
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

func TestSaveApprovedToTM_PopulatesTM(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})

	humanText := "Hallo"
	mq.approvedForTM[fileLangKey(1, "de")] = []db.ListApprovedForTMRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &humanText},
	}
	mq.settingsWriteTMs[langKey(projectID, "en", "de")] = []int64{50}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "de")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if res.ApprovedCount != 1 || res.TMSavedCount != 1 || res.NoWriteTMs {
		t.Errorf("result = %+v, want 1 approved, 1 saved", res)
	}
	if len(mq.upsertTMCalls) != 1 {
		t.Fatalf("expected 1 TM upsert, got %d", len(mq.upsertTMCalls))
	}
	call := mq.upsertTMCalls[0]
	if call.TmID != 50 || call.SourceText != "Hello" || call.TargetText != "Hallo" {
		t.Errorf("upsert = TM %d %q → %q, want TM 50 Hello → Hallo", call.TmID, call.SourceText, call.TargetText)
	}
}

func TestSaveApprovedToTM_MultipleWriteTMs(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})

	humanText := "Hallo"
	mq.approvedForTM[fileLangKey(1, "de")] = []db.ListApprovedForTMRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &humanText},
	}
	mq.settingsWriteTMs[langKey(projectID, "en", "de")] = []int64{50, 51}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "de")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if len(mq.upsertTMCalls) != 2 || mq.upsertTMCalls[0].TmID != 50 || mq.upsertTMCalls[1].TmID != 51 {
		t.Fatalf("expected upserts into TM 50 then 51, got %+v", mq.upsertTMCalls)
	}
	if res.TMSavedCount != 2 {
		t.Errorf("TMSavedCount = %d, want 2 (one per write TM)", res.TMSavedCount)
	}
}

func TestSaveApprovedToTM_SkipsNilText(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})

	humanText := "Hallo"
	mq.approvedForTM[fileLangKey(1, "de")] = []db.ListApprovedForTMRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &humanText},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World", ActiveText: nil},
	}
	mq.settingsWriteTMs[langKey(projectID, "en", "de")] = []int64{50}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "de")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if len(mq.upsertTMCalls) != 1 || mq.upsertTMCalls[0].SourceText != "Hello" {
		t.Fatalf("expected only 'Hello' upserted, got %+v", mq.upsertTMCalls)
	}
	if res.ApprovedCount != 2 {
		t.Errorf("ApprovedCount = %d, want 2 (both rows are approved)", res.ApprovedCount)
	}
}

// A file with no write TM for its pair warns once, not once per approved row.
func TestSaveApprovedToTM_NoWriteTMs(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"}) // no write TM assignments

	humanText := "Hallo"
	mq.approvedForTM[fileLangKey(1, "de")] = []db.ListApprovedForTMRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello", ActiveText: &humanText},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World", ActiveText: &humanText},
	}

	var buf bytes.Buffer
	prev := logging.L()
	logging.Init(logging.Options{ECS: true, Writer: &buf})
	t.Cleanup(func() { logging.SetLogger(prev) })

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "de")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if len(mq.upsertTMCalls) != 0 {
		t.Errorf("expected 0 TM upserts (no write TMs), got %d", len(mq.upsertTMCalls))
	}
	if !res.NoWriteTMs {
		t.Error("NoWriteTMs = false, want true so the caller can warn")
	}
	if n := strings.Count(buf.String(), "no-write-tm"); n != 1 {
		t.Errorf("got %d no-write-TM warnings, want 1: %s", n, buf.String())
	}
}

// A TM assigned to "fr-fr" still receives the write-back for a project on
// "fr".
func TestSaveApprovedToTM_BaseLangFallback(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"fr"})

	humanText := "Bonjour"
	mq.approvedForTM[fileLangKey(1, "fr")] = []db.ListApprovedForTMRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "fr", SourceText: "Hello", ActiveText: &humanText},
	}
	mq.settingsWriteTMs[langKey(projectID, "en", "fr-fr")] = []int64{50}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if len(mq.upsertTMCalls) != 1 || mq.upsertTMCalls[0].TmID != 50 {
		t.Fatalf("expected 1 upsert into TM 50 via base-language fallback, got %+v", mq.upsertTMCalls)
	}
	if res.NoWriteTMs {
		t.Error("expected NoWriteTMs=false, got true")
	}
}

func TestSaveApprovedToTM_EmptyRows_NoOp(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	res, err := svc.SaveApprovedToTM(context.Background(), 1, 1, "en", "de")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if len(mq.upsertTMCalls) != 0 || res.ApprovedCount != 0 {
		t.Errorf("expected nothing written, got %d upserts, result %+v", len(mq.upsertTMCalls), res)
	}
}
