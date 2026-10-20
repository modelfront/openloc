package mcp

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/translation"
)

// Segment-level job_ops tools. Every method starts from the translation (or
// job) helper, which applies the tenant scope and the linguist assignment gate.

// segmentOps returns the services and the checked translation.
func (b *directBackend) segmentOps(ctx context.Context, translationID int64) (*JobOpsServices, db.Translation, error) {
	s, err := b.jobServices()
	if err != nil {
		return nil, db.Translation{}, err
	}
	t, err := b.translation(ctx, translationID)
	if err != nil {
		return nil, db.Translation{}, err
	}
	return s, t, nil
}

func (b *directBackend) GetSegmentHistory(ctx context.Context, in TranslationRefIn) (SegmentHistory, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return SegmentHistory{}, err
	}
	tenantID, _ := scope(ctx)
	events, err := s.Translations.History(ctx, tenantID, t.ID)
	if err != nil {
		return SegmentHistory{}, serviceErr(err, "segment history")
	}
	out := SegmentHistory{TranslationID: t.ID, Events: make([]HistoryEntry, 0, len(events))}
	for _, e := range events {
		// No emails: the MCP surface never returns member addresses.
		out.Events = append(out.Events, HistoryEntry{
			Kind:          e.Kind,
			Text:          ptr.Deref(e.Text),
			CreatedAt:     e.CreatedAt,
			CreatedByName: ptr.Deref(e.CreatedByName),
			StepName:      ptr.Deref(e.StepName),
			TMName:        ptr.Deref(e.TMName),
			TMMatch:       e.TMMatchPercent,
			MTEngineName:  ptr.Deref(e.MTEngineName),
			EditDistance:  e.EditDistance,
		})
	}
	return out, nil
}

func (b *directBackend) ListComments(ctx context.Context, in TranslationRefIn) (CommentList, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return CommentList{}, err
	}
	tenantID, _ := scope(ctx)
	rows, err := s.Comments.List(ctx, tenantID, t.ID)
	if err != nil {
		return CommentList{}, serviceErr(err, "list comments")
	}
	out := CommentList{TranslationID: t.ID, Comments: make([]Comment, 0, len(rows))}
	for _, c := range rows {
		out.Comments = append(out.Comments, Comment{
			CommentID:  c.ID,
			Content:    c.Content,
			AuthorName: ptr.Deref(c.AuthorName),
			IsAI:       c.UserID == nil,
			CreatedAt:  tsString(c.CreatedAt),
		})
	}
	return out, nil
}

// SaveDraftsBatch saves many drafts through the batch-draft service: each row
// lands in needs_review, nothing is written to translation memory, and each
// row reports its own outcome. Locked segments and segments another user
// holds are never written.
func (b *directBackend) SaveDraftsBatch(ctx context.Context, in SaveDraftsBatchIn) (DraftBatchResult, error) {
	if len(in.Drafts) == 0 {
		return DraftBatchResult{}, invalidf("drafts is required")
	}
	if len(in.Drafts) > translation.MaxBatchDraftFixes {
		return DraftBatchResult{}, invalidf("too many drafts in one call (%d, max %d)", len(in.Drafts), translation.MaxBatchDraftFixes)
	}
	s, err := b.jobServices()
	if err != nil {
		return DraftBatchResult{}, err
	}
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return DraftBatchResult{}, err
	}
	fixes := make([]translation.DraftFix, len(in.Drafts))
	for i, d := range in.Drafts {
		expected, err := parseExpectedUpdatedAt(d.ExpectedUpdatedAt)
		if err != nil {
			return DraftBatchResult{}, err
		}
		text := d.Text
		if strings.TrimSpace(text) == "" {
			text = "" // rejected by the service, reported per row
		}
		fixes[i] = translation.DraftFix{TranslationID: d.TranslationID, HumanText: text, ExpectedUpdatedAt: expected}
	}
	tenantID, userID := scope(ctx)
	res, err := s.Translations.BatchDraft(ctx, tenantID, userID, j.FileID, fixes, false)
	if err != nil {
		return DraftBatchResult{}, serviceErr(err, "save drafts")
	}
	out := DraftBatchResult{
		Applied:   make([]SegmentWriteResult, 0, len(res.Applied)),
		Conflicts: res.Conflicts,
		Locked:    res.Locked,
		Held:      res.Held,
		Rejected:  res.Rejected,
	}
	for _, t := range res.Applied {
		out.Applied = append(out.Applied, SegmentWriteResult{
			TranslationID: t.ID, Status: string(t.Status), UpdatedAt: tsVersion(t.UpdatedAt),
		})
	}
	return out, nil
}

// RunSegmentQA re-runs QA for one segment against its stored text and
// persists the findings — the same service the editor calls after autosave.
func (b *directBackend) RunSegmentQA(ctx context.Context, in TranslationRefIn) (QAFindingList, error) {
	t, err := b.translation(ctx, in.TranslationID)
	if err != nil {
		return QAFindingList{}, err
	}
	tenantID, _ := scope(ctx)
	if _, err := b.qaSvc.RunAndPersistTranslation(ctx, t.ID, tenantID); err != nil {
		return QAFindingList{}, serviceErr(err, "run segment qa")
	}
	// Unscoped by signature; the id comes from the tenant-scoped lookup.
	rows, err := b.q.ListQAResultsForTranslation(ctx, t.ID)
	if err != nil {
		return QAFindingList{}, fmt.Errorf("list qa results: %w", err)
	}
	out := QAFindingList{TranslationID: t.ID, Findings: make([]QAIssue, 0, len(rows))}
	for _, r := range rows {
		out.Findings = append(out.Findings, QAIssue{
			TranslationID: r.TranslationID,
			CheckType:     r.CheckType,
			Severity:      r.Severity,
			Message:       r.Message,
			Ignored:       r.Ignored,
			IgnoredBy:     ptr.Deref(r.IgnoredByName),
			IgnoredAt:     tsString(r.IgnoredAt),
		})
	}
	return out, nil
}
