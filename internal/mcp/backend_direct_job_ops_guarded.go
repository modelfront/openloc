package mcp

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"openloc/internal/comments"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/job"
	"openloc/internal/ptr"
	"openloc/internal/translation"
)

// Guarded job_ops tools. Each Preview runs the same
// checks as its action and changes nothing.

// maxPreviewSamples caps how many example segments a preview lists.
const maxPreviewSamples = 10

// clip shortens text for a preview line.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// --- Find and replace ---------------------------------------------------------

func (b *directBackend) findReplaceParams(in FindReplaceJobIn, mode string) (translation.FindReplaceParams, error) {
	if in.Find == "" {
		return translation.FindReplaceParams{}, invalidf("find is required")
	}
	return translation.FindReplaceParams{
		Find: in.Find, Replace: in.Replace, CaseSensitive: in.CaseSensitive, Mode: mode,
		IncludeLocked: in.IncludeLocked,
	}, nil
}

func (b *directBackend) PreviewFindReplaceJob(ctx context.Context, in FindReplaceJobIn) (Preview, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return Preview{}, err
	}
	p, err := b.findReplaceParams(in, translation.FindModeFind)
	if err != nil {
		return Preview{}, err
	}
	tenantID, userID := scope(ctx)
	res, err := s.Translations.FindReplace(ctx, tenantID, userID, j, p)
	if err != nil {
		return Preview{}, serviceErr(err, "find")
	}
	if res.Segments == 0 {
		return Preview{}, invalidf("%q does not occur in this job's translations; nothing to replace", in.Find)
	}
	effects := []string{
		"The changed segments are saved as unconfirmed drafts, with their inline tags kept. Nothing is written to translation memory.",
	}
	if locked := countLocked(res.Found); locked > 0 {
		if translation.MayWriteLocked(ctx) {
			effects = append(effects, fmt.Sprintf("%d locked segment(s) match and will be overwritten and unlocked.", locked))
		} else {
			effects = append(effects, fmt.Sprintf("%d locked segment(s) match and will be left as they are: only an owner or admin can replace text in a locked segment.", locked))
		}
	}
	for i, m := range res.Found {
		if i == maxPreviewSamples {
			effects = append(effects, fmt.Sprintf("…and %d more segment(s).", len(res.Found)-maxPreviewSamples))
			break
		}
		effects = append(effects, fmt.Sprintf("Segment %d: “%s” → “%s”", m.TranslationID, clip(m.OldText, 80), clip(m.NewText, 80)))
	}
	return Preview{
		Summary: fmt.Sprintf("Replace “%s” with “%s” in %d segment(s) (%d match(es)) of job %d (%s).",
			in.Find, in.Replace, res.Segments, res.Matches, j.ID, j.TargetLanguage),
		Effects: effects,
	}, nil
}

func (b *directBackend) FindReplaceJob(ctx context.Context, in FindReplaceJobIn) (FindReplaceResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return FindReplaceResult{}, err
	}
	p, err := b.findReplaceParams(in, translation.FindModeReplaceAll)
	if err != nil {
		return FindReplaceResult{}, err
	}
	tenantID, userID := scope(ctx)
	res, err := s.Translations.FindReplace(ctx, tenantID, userID, j, p)
	if err != nil {
		return FindReplaceResult{}, serviceErr(err, "find and replace")
	}
	return FindReplaceResult{
		JobID: j.ID, Replaced: res.Replaced, Segments: res.Segments,
		Locked: res.Locked, Held: res.Held, SkippedTags: res.SkippedTags, Conflicts: res.Conflicts, Failed: res.Failed,
	}, nil
}

func countLocked(found []translation.FindMatch) int {
	n := 0
	for _, m := range found {
		if m.Locked {
			n++
		}
	}
	return n
}

// --- Clear a job's translations ---------------------------------------------

func (b *directBackend) PreviewClearJobTranslations(ctx context.Context, in ClearJobTranslationsIn) (Preview, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return Preview{}, err
	}
	if err := s.Jobs.PreflightClearTranslations(ctx, j, in.Scope); err != nil {
		return Preview{}, serviceErr(err, "clear translations")
	}
	counts, err := b.q.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{
		FileID: j.FileID, TargetLanguage: j.TargetLanguage,
	})
	if err != nil {
		return Preview{}, fmt.Errorf("count segments: %w", err)
	}
	n, what := counts.ClearableNonConfirmed, "unconfirmed segment(s)"
	if in.Scope == "all" {
		n, what = counts.ClearableAll, "segment(s), confirmed ones included"
	}
	effects := []string{
		"Their text, QA findings and AI comments are removed. Human comments, locked segments and segments another user has open are kept.",
	}
	if j.CurrentStepID == nil {
		effects = append(effects, "The workflow is finished; clearing reopens its first step.")
	}
	return Preview{
		Summary: fmt.Sprintf("Clear %d %s in job %d (%s).", n, what, j.ID, j.TargetLanguage),
		Effects: effects,
	}, nil
}

func (b *directBackend) ClearJobTranslations(ctx context.Context, in ClearJobTranslationsIn) (job.ClearResult, error) {
	s, j, _, err := b.resolvedJob(ctx, in.JobID)
	if err != nil {
		return job.ClearResult{}, err
	}
	res, err := s.Jobs.ClearTranslations(ctx, j, in.Scope)
	if err != nil {
		return job.ClearResult{}, serviceErr(err, "clear translations")
	}
	return res, nil
}

// --- Clear one segment --------------------------------------------------------

// segmentFile is the file a checked translation's segment belongs to.
func (b *directBackend) segmentFile(ctx context.Context, t db.Translation) (int64, error) {
	// Unscoped by signature; the id comes from the tenant-scoped lookup.
	fileID, err := b.q.GetTranslationFileID(ctx, t.ID)
	if err != nil {
		return 0, fmt.Errorf("get translation file: %w", err)
	}
	return fileID, nil
}

func (b *directBackend) PreviewClearSegment(ctx context.Context, in ClearSegmentIn) (Preview, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return Preview{}, err
	}
	fileID, err := b.segmentFile(ctx, t)
	if err != nil {
		return Preview{}, err
	}
	tenantID, userID := scope(ctx)
	if _, err := s.Translations.PreflightClear(ctx, tenantID, userID, fileID, t.ID); err != nil {
		return Preview{}, serviceErr(err, "clear segment")
	}
	if t.ActiveText == nil || *t.ActiveText == "" {
		return Preview{}, invalidf("this segment has no translation; nothing to clear")
	}
	return Preview{
		Summary: fmt.Sprintf("Clear the translation of segment %d: “%s”.", t.ID, clip(*t.ActiveText, 120)),
		Effects: []string{"The segment becomes untranslated; its QA findings and AI comments are removed. Human comments are kept."},
	}, nil
}

func (b *directBackend) ClearSegment(ctx context.Context, in ClearSegmentIn) (SegmentWriteResult, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return SegmentWriteResult{}, err
	}
	fileID, err := b.segmentFile(ctx, t)
	if err != nil {
		return SegmentWriteResult{}, err
	}
	tenantID, userID := scope(ctx)
	res, err := s.Translations.Clear(ctx, tenantID, userID, fileID, t.ID)
	if err != nil {
		return SegmentWriteResult{}, serviceErr(err, "clear segment")
	}
	cleared := res.Translation
	return SegmentWriteResult{TranslationID: cleared.ID, Status: string(cleared.Status), UpdatedAt: tsVersion(cleared.UpdatedAt)}, nil
}

// --- Delete a comment ---------------------------------------------------------

func (b *directBackend) PreviewDeleteComment(ctx context.Context, in DeleteCommentIn) (Preview, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return Preview{}, err
	}
	tenantID, userID := scope(ctx)
	c, err := s.Comments.Get(ctx, tenantID, t.ID, in.CommentID)
	if err != nil {
		return Preview{}, serviceErr(err, "get comment")
	}
	if !comments.MayDelete(c, userID) {
		return Preview{}, invalidf("only the comment's author can delete it; AI comments can be deleted by anyone")
	}
	author := "the AI"
	if c.UserID != nil {
		author = ptr.Deref(c.AuthorName)
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the comment by %s on segment %d: “%s”.", author, t.ID, clip(c.Content, 120)),
	}, nil
}

func (b *directBackend) DeleteComment(ctx context.Context, in DeleteCommentIn) (DeleteCommentResult, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return DeleteCommentResult{}, err
	}
	tenantID, userID := scope(ctx)
	if err := s.Comments.Delete(ctx, tenantID, userID, t.ID, in.CommentID); err != nil {
		return DeleteCommentResult{}, serviceErr(err, "delete comment")
	}
	return DeleteCommentResult{CommentID: in.CommentID, Deleted: true}, nil
}

// --- Confirm a translation ----------------------------------------------------

// confirmParams builds the assistant confirm. Empty text confirms the
// segment's current target text.
func (b *directBackend) confirmParams(ctx context.Context, in ConfirmTranslationIn, t db.Translation) (translation.ConfirmParams, error) {
	text := in.Text
	if strings.TrimSpace(text) == "" {
		text = ptr.Deref(t.ActiveText)
	}
	if strings.TrimSpace(text) == "" {
		return translation.ConfirmParams{}, invalidf("this segment has no translation to confirm; pass text")
	}
	expected, err := parseExpectedUpdatedAt(in.ExpectedUpdatedAt)
	if err != nil {
		return translation.ConfirmParams{}, err
	}
	tenantID, userID := scope(ctx)
	return translation.ConfirmParams{
		ID: t.ID, TenantID: tenantID, UserID: userID,
		HumanText: text, HumanRichText: translation.KeptRichText(t, text),
		ExpectedUpdatedAt: expected,
		Via:               translation.ConfirmedViaAssistant,
	}, nil
}

func (b *directBackend) PreviewConfirmTranslation(ctx context.Context, in ConfirmTranslationIn) (Preview, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return Preview{}, err
	}
	p, err := b.confirmParams(ctx, in, t)
	if err != nil {
		return Preview{}, err
	}
	if _, err := s.Translations.PreflightConfirm(ctx, p); err != nil {
		return Preview{}, serviceErr(err, "confirm")
	}
	// Unscoped by signature; the id comes from the tenant-scoped lookup.
	seg, err := b.q.GetSegment(ctx, t.SegmentID)
	if err != nil {
		return Preview{}, fmt.Errorf("get segment: %w", err)
	}
	return Preview{
		Summary: fmt.Sprintf("Confirm segment %d.", t.ID),
		Effects: []string{
			"Source: " + clip(seg.SourceText, 300),
			"Translation: " + clip(p.HumanText, 300),
			"The segment becomes confirmed, marked as confirmed by the assistant. It is not written to translation memory until a person confirms it.",
		},
	}, nil
}

func (b *directBackend) ConfirmTranslation(ctx context.Context, in ConfirmTranslationIn) (SegmentWriteResult, error) {
	s, t, err := b.segmentOps(ctx, in.TranslationID)
	if err != nil {
		return SegmentWriteResult{}, err
	}
	p, err := b.confirmParams(ctx, in, t)
	if err != nil {
		return SegmentWriteResult{}, err
	}
	res, err := s.Translations.Confirm(ctx, p)
	if err != nil {
		return SegmentWriteResult{}, serviceErr(err, "confirm")
	}
	return SegmentWriteResult{
		TranslationID:    res.Translation.ID,
		Status:           string(res.Translation.Status),
		UpdatedAt:        tsVersion(res.Translation.UpdatedAt),
		TMEntriesWritten: len(res.UpdatedTMIDs),
	}, nil
}
