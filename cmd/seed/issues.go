package main

import (
	"context"
	"log"
	"strconv"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/seedid"
)

// seedOperator ensures the dev platform admin exists and is an admin of the
// second org (acmeID). PLATFORM_ADMIN_EMAILS in the Makefile names this user,
// who reads reported issues. Returns the user id, 0 when it could not be made.
func seedOperator(ctx context.Context, q *db.Queries, acmeID int64) int64 {
	if acmeID == 0 {
		return 0
	}
	user, err := q.GetUserByEmail(ctx, seedid.OperatorEmail)
	if err != nil {
		user, err = q.CreateUser(ctx, db.CreateUserParams{Email: seedid.OperatorEmail, Name: seedid.OperatorName})
		if err != nil {
			log.Printf("⚠ Failed to create the operator: %v", err)
			return 0
		}
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: user.ID, TenantID: acmeID, Role: "admin"}); err != nil {
		log.Printf("⚠ Failed to create the operator's membership: %v", err)
		return 0
	}
	log.Printf("✓ Operator: %s (platform admin in dev)", seedid.OperatorEmail)
	return user.ID
}

// seedDemoIssues gives the owner two reported issues, one waiting for the team
// and one where the team asked a question, plus one from the seed linguist, so
// Reported issues has something to show for every role. Each reporter's issues
// are skipped once they have any.
func seedDemoIssues(ctx context.Context, q *db.Queries, tenantID, ownerID, operatorID int64) {
	if operatorID == 0 {
		return
	}
	seedVendorIssue(ctx, q, tenantID)
	if hasIssues(ctx, q, tenantID, ownerID) {
		log.Printf("✓ Demo issues exist")
		return
	}
	ctxJSON := []byte(`{"path":"/projects","role":"owner","browser":"Chrome 131","os":"macOS","viewport":"1440x900"}`)
	major := "major"
	if _, err := q.CreateIssue(ctx, db.CreateIssueParams{
		TenantID: tenantID, ReporterID: ownerID, Kind: "bug", Severity: &major, Area: "editor",
		Title:       "Exported DOCX loses the table borders",
		Description: "I exported the translated contract and the tables in the DOCX have no borders. The source file has them.",
		Context:     ctxJSON,
	}); err != nil {
		log.Printf("⚠ Failed to create a demo issue: %v", err)
		return
	}
	question, err := q.CreateIssue(ctx, db.CreateIssueParams{
		TenantID: tenantID, ReporterID: ownerID, Kind: "enhancement", Area: "terminology",
		Title:       "Import glossaries from other tools",
		Description: "We keep our terminology in another TMS. Please let us bring it into OpenLoc.",
		Context:     ctxJSON,
	})
	if err != nil {
		log.Printf("⚠ Failed to create a demo issue: %v", err)
		return
	}
	body, status := "TBX import may already cover it. Which tool will you export it from?", "needs_info"
	if _, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: question.ID, AuthorID: &operatorID, AuthorSide: "operator", Body: &body, Status: &status,
	}); err != nil {
		log.Printf("⚠ Failed to create a demo issue comment: %v", err)
		return
	}
	operator := "operator"
	if _, err := q.UpdateIssueActivity(ctx, db.UpdateIssueActivityParams{ID: question.ID, Status: &status, LastActivityBy: &operator}); err != nil {
		log.Printf("⚠ Failed to update a demo issue: %v", err)
		return
	}
	seedSupportFields(ctx, q, question.ID, operatorID)
	log.Printf("✓ Demo issues: 2 for %s", seedid.OwnerEmail)
}

// hasIssues reports whether the user already reported an issue in the org (or
// the check failed, which also means: seed nothing).
func hasIssues(ctx context.Context, q *db.Queries, tenantID, userID int64) bool {
	existing, err := q.ListMemberIssuesNewest(ctx, db.ListMemberIssuesNewestParams{
		TenantID: tenantID, ViewerID: userID, Statuses: []string{}, Kinds: []string{},
		Areas: []string{}, Severities: []string{}, RowLimit: 1,
	})
	if err != nil {
		log.Printf("⚠ Failed to check demo issues: %v", err)
		return true
	}
	return len(existing) > 0
}

// seedVendorIssue files one issue as the seed linguist, so a manager's
// Reported issues shows a colleague's report next to their own.
func seedVendorIssue(ctx context.Context, q *db.Queries, tenantID int64) {
	vendor, err := q.GetUserByEmail(ctx, seedid.VendorEmail)
	if err != nil || hasIssues(ctx, q, tenantID, vendor.ID) {
		return
	}
	minor := "minor"
	if _, err := q.CreateIssue(ctx, db.CreateIssueParams{
		TenantID: tenantID, ReporterID: vendor.ID, Kind: "bug", Severity: &minor, Area: "qa",
		Title:       "QA flags a number that is the same in both languages",
		Description: "The number check marks 1.5 as missing in the German target, where it is written 1,5.",
		Context:     []byte(`{"path":"/tasks","role":"linguist","browser":"Firefox 131","os":"Windows","viewport":"1920x1080"}`),
	}); err != nil {
		log.Printf("⚠ Failed to create a demo issue: %v", err)
		return
	}
	log.Printf("✓ Demo issue: 1 for %s", seedid.VendorEmail)
}

// seedSupportFields gives a demo issue the support team's side: an assignee,
// a priority, tags and an internal note, so /platform/issues shows them.
func seedSupportFields(ctx context.Context, q *db.Queries, issueID, operatorID int64) {
	priority, note, field := "medium", "Seed note: TBX import covers most tools; check which one they use.", "assignee"
	assignee := strconv.FormatInt(operatorID, 10)
	if _, err := q.SetIssueAssignee(ctx, db.SetIssueAssigneeParams{ID: issueID, AssigneeID: &operatorID}); err != nil {
		log.Printf("⚠ Failed to assign a demo issue: %v", err)
		return
	}
	if _, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: issueID, AuthorID: &operatorID, AuthorSide: "operator", Field: &field, ToValue: &assignee,
	}); err != nil {
		log.Printf("⚠ Failed to record a demo assignment: %v", err)
		return
	}
	if _, err := q.SetIssuePriority(ctx, db.SetIssuePriorityParams{ID: issueID, Priority: &priority}); err != nil {
		log.Printf("⚠ Failed to prioritize a demo issue: %v", err)
		return
	}
	if _, err := q.SetIssueTags(ctx, db.SetIssueTagsParams{ID: issueID, Tags: []string{"terminology", "import"}}); err != nil {
		log.Printf("⚠ Failed to tag a demo issue: %v", err)
		return
	}
	if _, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: issueID, AuthorID: &operatorID, AuthorSide: "operator", Body: &note, Internal: true,
	}); err != nil {
		log.Printf("⚠ Failed to add a demo internal note: %v", err)
	}
}
