package translation

import (
	"context"
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/qa"
)

// QAChecker runs QA against a proposed human translation and reports
// issues without persisting anything. Service.checkQA uses this on
// every confirm to enforce QA blocking server-side.
// Frontend-only blocking is bypassable
// via curl, so the load-bearing gate is here.
//
// Returning a result with ProfileAssigned=false is the "no enforcement
// possible" signal — the caller (this handler) treats it as graceful
// degradation and allows the save. A project that hasn't been assigned
// a QA profile shouldn't trap translators behind an undefined gate.
type QAChecker interface {
	CheckTranslationProposal(ctx context.Context, params qa.CheckProposalParams) (qa.CheckResult, error)
	// JobChecker checks many rows of one job with one rule load (bulk confirm).
	JobChecker(ctx context.Context, job db.Job, tenantID int64) (*qa.JobChecker, error)
}

// qaBlockErrorBody is the typed shape of the 422 envelope returned when
// QA blocks a confirm save. Pinned at the Go type level so a rename of
// `qa_issues` (or any other field) breaks at compile time — every FE
// client depends on this exact wire shape via `getQAIssuesFromError`.
//
// The "code" field is load-bearing (see qa-block.ts::isQABlockError):
// the FE distinguishes QA blocks from other 422s (terminology MT-engine
// unavailable, future cases) via `error.code === "QAErrorsBlocked"`.
type qaBlockErrorBody struct {
	Error    qaBlockErrorEnvelope `json:"error"`
	QAIssues []qa.Issue           `json:"qa_issues"`
}

type qaBlockErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// QAErrorsBlockedCode is the identity string the FE matches on to
// classify a 422 as a QA block. Public so contract tests can pin the
// exact string and so the FE's `isQABlockError` source comment can
// reference it.
const QAErrorsBlockedCode = "QAErrorsBlocked"

// writeQABlocked writes the 422 for a confirm that QA errors block. 422 is
// semantically right: the request is well-formed, but a domain rule (QA
// error severity) rejects it. The body carries the issue list so the FE can
// surface them without a follow-up GET. The gate itself is Service.checkQA.
func writeQABlocked(w http.ResponseWriter, issues []qa.Issue) {
	httputil.JSON(w, http.StatusUnprocessableEntity, qaBlockErrorBody{
		Error: qaBlockErrorEnvelope{
			Code:    QAErrorsBlockedCode,
			Message: "fix QA errors before confirming",
		},
		QAIssues: issues,
	})
}
