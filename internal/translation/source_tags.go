package translation

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parsers"
	"openloc/internal/parsers/common"
	"openloc/internal/ptr"
	"openloc/internal/qa"
)

// SourceTagsCheckType is the QA check type of a tag the source does not have.
const SourceTagsCheckType = "tag_not_in_source"

// foreignTagIssues returns one QA error per tag of the proposed text that the
// source does not have, in a JSON, PO, TXT or XLSX file whose strings are
// HTML: there the export writes the text as is, so a tag in it becomes markup
// in the customer's file, and a confirmed translation holds only the tags its
// source has, whether or not the project has a QA profile. The text is read
// as the export reads it, so the check refuses exactly what the export writes
// as text. A file whose strings are plain text has no tags to check.
func foreignTagIssues(file db.File, sourceText string, sourceRich *string, text string, rich *string) []qa.Issue {
	if !file.InlineHtml || !parsers.InlineHTMLFormat(string(file.Format)) {
		return nil
	}
	source := sourceText
	if r := ptr.Deref(sourceRich); r != "" {
		source = common.XLIFFInlineToHTML(r)
	}
	var issues []qa.Issue
	for _, tag := range common.ForeignTags(parsers.TextFormatTranslation(text, ptr.Deref(rich), true), source) {
		issues = append(issues, qa.Issue{
			CheckType: SourceTagsCheckType,
			Severity:  "error",
			Message:   fmt.Sprintf("Tag %s is not in the source", tag),
		})
	}
	return issues
}

// checkSourceTags refuses a confirm whose text has a tag the source does not
// have, in a file whose export writes HTML strings as is.
func (s *Service) checkSourceTags(ctx context.Context, t db.Translation, text string, rich *string) error {
	// Unscoped by signature; the translation was loaded tenant-scoped.
	seg, err := s.queries.GetSegment(ctx, t.SegmentID)
	if err != nil {
		return fmt.Errorf("get segment: %w", err)
	}
	file, err := s.queries.GetFile(ctx, seg.FileID)
	if err != nil {
		return fmt.Errorf("get file: %w", err)
	}
	if issues := foreignTagIssues(file, seg.SourceText, seg.SourceRichText, text, rich); len(issues) > 0 {
		return &QABlockedError{Issues: issues}
	}
	return nil
}
