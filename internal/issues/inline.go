package issues

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"

	db "openloc/internal/database/sqlcgen"
)

// An image shown inside a description or a comment is a reference in its
// text, where the image goes. The client sends `![name](upload:N)`, N being
// the image's place among the request's files; once the files are stored,
// the text keeps `![name](attachment:ID)`. The text stays plain text: the
// page shows the image at that place, and a reference to a file the
// description or comment does not carry stays text.
var (
	uploadRef = regexp.MustCompile(`!\[([^\]\n]{0,255})\]\(upload:(\d{1,3})\)`)
	inlineRef = regexp.MustCompile(`!\[([^\]\n]{0,255})\]\((?:upload|attachment):(\d{1,19})\)`)
)

// checkUploadRefs refuses a text whose image references point past the
// request's files: what the request carries is known before anything is
// stored.
func checkUploadRefs(text string, files []Upload) error {
	for _, m := range uploadRef.FindAllStringSubmatch(text, -1) {
		if n, _ := strconv.Atoi(m[2]); n >= len(files) {
			return clientError("An image in the text is not among the attached files.")
		}
	}
	return nil
}

// linkUploadRefs turns the text's upload references into references to the
// stored files (views, in upload order), and checks each points at an image
// and the result still fits maxText. ok is false when the text has none.
func linkUploadRefs(text string, views []AttachmentView) (out string, ok bool, err error) {
	if !uploadRef.MatchString(text) {
		return text, false, nil
	}
	out = uploadRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := uploadRef.FindStringSubmatch(ref)
		n, _ := strconv.Atoi(m[2])
		if n >= len(views) || !views[n].IsImage {
			if err == nil {
				err = clientError("Only images can be shown in the text. Attach other files below it.")
			}
			return ref
		}
		return fmt.Sprintf("![%s](attachment:%d)", m[1], views[n].ID)
	})
	if err != nil {
		return "", false, err
	}
	if utf8.RuneCountInString(out) > maxText {
		return "", false, clientError(fmt.Sprintf("The text is too long with its images (max %d characters).", maxText))
	}
	return out, true, nil
}

// recordCommentFiles records a new comment's files and points its text's
// image references at them. Runs inside the caller's transaction.
func recordCommentFiles(ctx context.Context, q *db.Queries, issueID, commentID int64, body *string, files []storedFile) error {
	views, err := recordFiles(ctx, q, issueID, &commentID, files)
	if err != nil || body == nil {
		return err
	}
	linked, ok, err := linkUploadRefs(*body, views)
	if err != nil || !ok {
		return err
	}
	if err := q.SetIssueCommentBody(ctx, db.SetIssueCommentBodyParams{ID: commentID, Body: &linked}); err != nil {
		return fmt.Errorf("link comment images: %w", err)
	}
	return nil
}

// linkDescriptionImages points a new issue's description images at its
// stored files. Runs inside the transaction that inserted the issue.
func linkDescriptionImages(ctx context.Context, q *db.Queries, issue db.Issue, views []AttachmentView) (db.Issue, error) {
	linked, ok, err := linkUploadRefs(issue.Description, views)
	if err != nil || !ok {
		return issue, err
	}
	updated, err := q.SetIssueDescription(ctx, db.SetIssueDescriptionParams{ID: issue.ID, Description: linked})
	if err != nil {
		return db.Issue{}, fmt.Errorf("link description images: %w", err)
	}
	return updated, nil
}

// plainText is text for an email or the bell, where an image cannot show:
// each image reference becomes "[image: name]".
func plainText(text string) string {
	return inlineRef.ReplaceAllStringFunc(text, func(ref string) string {
		name := inlineRef.FindStringSubmatch(ref)[1]
		if name == "" {
			return "[image]"
		}
		return "[image: " + name + "]"
	})
}
