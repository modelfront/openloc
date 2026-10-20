package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/issues"
	"openloc/internal/logging"
)

// WireIssues hands the issue service to a backend built by
// NewDirectBackendWithPool. Without it the report tool answers that the
// server takes no reports.
func WireIssues(b Backend, svc *issues.Service) {
	if d, ok := b.(*directBackend); ok {
		d.issues = svc
	}
}

// newIssue is the report the arguments describe. The assistant's check is
// kept only from the built-in assistant: another MCP client's model is the
// reporter's own, so its check would be the reporter's claim.
func (b *directBackend) newIssue(ctx context.Context, in ReportIssueIn) (issues.NewIssue, error) {
	page, err := json.Marshal(map[string]string{"path": in.Page})
	if err != nil {
		return issues.NewIssue{}, fmt.Errorf("issue context: %w", err)
	}
	n := issues.NewIssue{
		Kind: in.Kind, Severity: in.Severity, Area: in.Area, Title: in.Title,
		Description: in.Description, Context: string(page), Via: issues.ViaAssistant,
	}
	if fromBuiltInAssistant(ctx) {
		n.Verification = issues.Verification{Result: in.Verification, Note: in.VerificationNote}
	}
	return n, nil
}

// member is the caller as the issue service knows them.
func member(ctx context.Context) (issues.Member, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return issues.Member{}, errDenied
	}
	return issues.Member{TenantID: claims.TenantID, UserID: claims.UserID, Role: claims.Role}, nil
}

// openScreenshot opens one of the caller's unexpired uploads that is an image
// of at most issues.MaxScreenshotSize, and returns its first bytes. The caller
// closes the body.
func (b *directBackend) openScreenshot(ctx context.Context, id string) (db.ChatUpload, []byte, io.ReadCloser, error) {
	row, body, err := b.openUpload(ctx, id)
	if errors.Is(err, errNotFound) {
		return row, nil, nil, invalidf("upload %s is not one of this user's screenshots, or it has expired", id)
	}
	if err != nil {
		return row, nil, nil, err
	}
	fail := func(err error) (db.ChatUpload, []byte, io.ReadCloser, error) {
		if cerr := body.Close(); cerr != nil {
			logging.Warn(ctx, cerr, "closing an assistant upload failed", "mcp-upload-close-failed")
		}
		return row, nil, nil, err
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(body, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return fail(fmt.Errorf("read upload: %w", err))
	}
	if _, image := issues.SniffImage(head[:n]); !image || row.SizeBytes > issues.MaxScreenshotSize {
		return fail(invalidf("upload %s is not a screenshot", id))
	}
	return row, head[:n], body, nil
}

// screenshotFiles reads the screenshots a report attaches. With read false it
// only checks them, as the preview does.
func (b *directBackend) screenshotFiles(ctx context.Context, ids []string, read bool) ([]issues.Upload, error) {
	if len(ids) > issues.MaxScreenshots {
		return nil, invalidf("attach at most %d screenshots", issues.MaxScreenshots)
	}
	out := make([]issues.Upload, 0, len(ids))
	for _, id := range ids {
		row, head, body, err := b.openScreenshot(ctx, id)
		if err != nil {
			return nil, err
		}
		var rest []byte
		if read {
			rest, err = io.ReadAll(io.LimitReader(body, issues.MaxScreenshotSize))
		}
		if cerr := body.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, fmt.Errorf("read upload: %w", err)
		}
		out = append(out, issues.BytesUpload(row.Filename, append(head, rest...)))
	}
	return out, nil
}

func (b *directBackend) issueService() (*issues.Service, error) {
	if b.issues == nil || !b.issues.Enabled() {
		return nil, invalidf("this OpenLoc installation does not take issue reports")
	}
	return b.issues, nil
}

func (b *directBackend) PreviewReportIssue(ctx context.Context, in ReportIssueIn) (Preview, error) {
	svc, err := b.issueService()
	if err != nil {
		return Preview{}, err
	}
	n, err := b.newIssue(ctx, in)
	if err != nil {
		return Preview{}, err
	}
	if _, err := b.screenshotFiles(ctx, in.AttachmentUploadIDs, false); err != nil {
		return Preview{}, err
	}
	if err := svc.Check(n); err != nil {
		return Preview{}, serviceErr(err, "check issue")
	}
	m, err := member(ctx)
	if err != nil {
		return Preview{}, err
	}
	token, err := svc.SignVerification(m, n.Verification)
	if err != nil {
		return Preview{}, serviceErr(err, "sign issue verification")
	}
	area := in.Area
	if area == "" {
		area = issues.AreaOther
	}
	return Preview{
		Summary: fmt.Sprintf("Send “%s” to the support team: %s, %s.", in.Title,
			issues.KindSummary(in.Kind, in.Severity), issues.AreaLabel(area)),
		Effects: []string{
			"The support team gets an email with the report, the page and your role.",
			"You follow the answer under Reported issues in the account menu.",
		},
		VerificationToken: token,
	}, nil
}

func (b *directBackend) ReportIssue(ctx context.Context, in ReportIssueIn) (ReportIssueResult, error) {
	svc, err := b.issueService()
	if err != nil {
		return ReportIssueResult{}, err
	}
	m, err := member(ctx)
	if err != nil {
		return ReportIssueResult{}, err
	}
	n, err := b.newIssue(ctx, in)
	if err != nil {
		return ReportIssueResult{}, err
	}
	if n.Files, err = b.screenshotFiles(ctx, in.AttachmentUploadIDs, true); err != nil {
		return ReportIssueResult{}, err
	}
	// The user confirmed these exact arguments, the check among them.
	if n.VerificationToken, err = svc.SignVerification(m, n.Verification); err != nil {
		return ReportIssueResult{}, serviceErr(err, "sign issue verification")
	}
	issue, err := svc.Create(ctx, m, n)
	if issues.IsRateLimited(err) {
		return ReportIssueResult{}, errLimited{err.Error()}
	}
	if err != nil {
		return ReportIssueResult{}, serviceErr(err, "report issue")
	}
	return ReportIssueResult{ID: issue.ID, Title: issue.Title, Status: issue.Status,
		Path: fmt.Sprintf("/issues/%d", issue.ID)}, nil
}
