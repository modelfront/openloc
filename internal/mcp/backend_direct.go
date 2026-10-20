package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/issues"
	"openloc/internal/langcode"
	"openloc/internal/mtengine"
	"openloc/internal/productdocs"
	"openloc/internal/ptr"
	"openloc/internal/qa"
	"openloc/internal/stepround"
	"openloc/internal/terminology"
)

// directBackend serves MCP tools from in-process queries.
//
// SECURITY — read this before adding a method.
//
// This backend bypasses the HTTP middleware chain that normally enforces access
// (auth.RequireJobOwnership, auth.RequireFileOwnership, auth.RequireManager,
// auth.RequireLinguistJobAccess). That makes it the single most dangerous file
// in the MCP surface: a method that forgets tenant scoping is a cross-tenant
// leak that no existing test would catch.
//
// Two rules, no exceptions:
//
//  1. Every query is either tenant-scoped by parameter (GetProject, GetJobTenant)
//     or tenant-scoped structurally by joining through projects.tenant_id
//     (GetMCPJobDetail, ListJobSegmentsPage). NEVER call the unscoped variants
//     GetProjectByID / GetFile / GetJob from here.
//  2. Where a follow-up query is unavoidably unscoped (ListJobsByProject,
//     ListQAResultsForJob), it runs ONLY on ids returned by a preceding
//     tenant-scoped lookup — never on an id straight from a tool argument.
//     This mirrors how the HTTP handlers do it (see qa.HandleQAReport).
//
// Role gates are applied one layer up, in server.go, where each tool declares
// the role it requires. The linguist assignment gate is the exception: it
// depends on the resource, so the `job` and `translation` helpers apply it.
// Any new job- or segment-taking method must go through those helpers.
type directBackend struct {
	q db.Querier
	// pool is optional; required only by paths that run transactions
	// (create_project's template application). nil in tests that don't
	// exercise those paths — they degrade to a model-visible error.
	pool *pgxpool.Pool
	// docs is optional; the documentation tools answer
	// with a model-visible error when it is nil.
	docs *productdocs.Index
	// qaSvc is the same QA service the REST handlers run, so openloc_run_qa
	// and openloc_ignore_qa_finding cannot drift from POST /run-qa and the
	// ignore routes.
	qaSvc *qa.Service
	// catalog gates which models create_ai_engine may pin on a row. Optional:
	// aiengine.Create substitutes a never-fetching catalog when it is nil, so a
	// test backend validates against the built-in lists rather than panicking.
	catalog *ai.Catalog
	// storage reads assistant uploads (chat_uploads) for the import tools.
	// Optional: those tools answer with a model-visible error when it is nil.
	storage file.BlobStore
	// ops are the REST services behind the project and file tools
	// (WireProjectOps). Zero in tests that don't wire them.
	ops ProjectOpsServices
	// insight carries what the analysis and invite tools need beyond the
	// Querier; see WithInsightDeps.
	insight InsightDeps
	// issues files issue reports (WireIssues). Optional: the report tool
	// answers that the server takes no reports when it is nil.
	issues *issues.Service
	// jobOps are the REST handlers' job/workflow/segment services (WireJobOps).
	// Optional: those tools answer with a model-visible error when it is nil.
	jobOps *JobOpsServices
	// dec resolves a vendor token's key, which MT router validation needs to
	// read a provider engine's catalog. Optional: without it a router saves
	// without the language check, same as an engine whose key is unreadable.
	dec mtengine.Decryptor
}

// NewDirectBackend builds the in-process Backend implementation.
func NewDirectBackend(q db.Querier) Backend {
	return &directBackend{q: q, qaSvc: qa.NewService(q)}
}

// NewDirectBackendWithPool is NewDirectBackend plus transaction support for
// the creation tools, the QA runs, and the documentation
// index for the docs tools. Production wiring uses this.
func NewDirectBackendWithPool(q db.Querier, pool *pgxpool.Pool, docs *productdocs.Index, catalog *ai.Catalog, dec mtengine.Decryptor, storage file.BlobStore) Backend {
	return &directBackend{q: q, pool: pool, docs: docs, qaSvc: qa.NewServiceWithPool(q, pool), catalog: catalog, dec: dec, storage: storage}
}

// errNotFound is the backend's "no such resource in your tenant" signal. It is
// deliberately the same error for "doesn't exist" and "belongs to another
// tenant" — the MCP layer turns it into one message, so a caller can't probe
// for the existence of other tenants' data.
var errNotFound = errors.New("not found")

// scope pulls the caller's tenant and user from the auth claims that
// auth.Middleware placed in the request context. The MCP SDK propagates request
// context values into tool handlers (it detaches cancellation but preserves
// values, explicitly so auth middleware works), so these are the same claims
// every REST handler sees.
func scope(ctx context.Context) (tenantID, userID int64) {
	return auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx)
}

func tsString(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// tsVersion formats a timestamp used as an OPTIMISTIC-LOCKING TOKEN, not as a
// human-readable date — hence full nanosecond precision.
//
// This distinction is load-bearing. Postgres timestamptz holds microseconds;
// plain RFC3339 truncates to whole seconds. Round-tripping a version through the
// truncated form means the equality check against updated_at never matches, so
// every write with expected_updated_at fails as a spurious conflict.
func tsVersion(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339Nano)
}

// --- Projects ---------------------------------------------------------------

func (b *directBackend) ListCustomers(ctx context.Context, in ListCustomersIn) (CustomerList, error) {
	tenantID, _ := scope(ctx)
	// Tenant-scoped by parameter, and the query already excludes soft-deleted rows.
	rows, err := b.q.ListCustomers(ctx, tenantID)
	if err != nil {
		return CustomerList{}, fmt.Errorf("list customers: %w", err)
	}
	limit := clampLimit(in.Limit)
	out := make([]Customer, 0, min(len(rows), limit))
	for _, c := range rows {
		if len(out) >= limit {
			break
		}
		out = append(out, Customer{ID: c.ID, Name: c.Name})
	}
	return CustomerList{Customers: out, Count: len(out)}, nil
}

// project is the tenant-scoped project lookup every project-taking tool funnels
// through. Centralised so no tool can accidentally skip the tenant check.
func (b *directBackend) project(ctx context.Context, projectID int64) (db.Project, error) {
	tenantID, _ := scope(ctx)
	p, err := b.q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Project{}, errNotFound
		}
		return db.Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// --- Jobs -------------------------------------------------------------------

// job is the tenant-scoped job lookup shared by every job-taking tool. The
// underlying query scopes structurally through jobs→files→projects.tenant_id;
// a linguist additionally needs an assignment to the job, as on REST.
func (b *directBackend) job(ctx context.Context, jobID int64) (db.GetMCPJobDetailRow, error) {
	tenantID, _ := scope(ctx)
	row, err := b.q.GetMCPJobDetail(ctx, db.GetMCPJobDetailParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetMCPJobDetailRow{}, errNotFound
		}
		return db.GetMCPJobDetailRow{}, fmt.Errorf("get job: %w", err)
	}
	ok, err := auth.LinguistMayAccessJob(ctx, b.q, row.JobID)
	if err != nil {
		return db.GetMCPJobDetailRow{}, fmt.Errorf("check job assignment: %w", err)
	}
	if !ok {
		return db.GetMCPJobDetailRow{}, errNotFound
	}
	return row, nil
}

func (b *directBackend) GetJob(ctx context.Context, in GetJobIn) (JobDetail, error) {
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return JobDetail{}, err
	}
	tenantID, _ := scope(ctx)
	steps, err := b.jobSteps(ctx, tenantID, []int64{j.JobID})
	if err != nil {
		return JobDetail{}, err
	}
	return JobDetail{
		JobSummary: JobSummary{
			ID:              j.JobID,
			FileID:          j.FileID,
			ProjectID:       j.ProjectID,
			ProjectName:     j.ProjectName,
			FileName:        j.FileName,
			TargetLanguage:  j.TargetLanguage,
			Status:          j.Status,
			CurrentStep:     j.CurrentStep,
			WordCount:       j.WordCount,
			SegmentCount:    j.SegmentCount,
			TranslatedCount: ptr.Deref(j.TranslatedCount),
			ErrorMessage:    ptr.Deref(j.ErrorMessage),
			MTSkipReason:    ptr.Deref(j.MtSkipReason),
			// The job's own deadline — its active step's, NOT the
			// project's; see JobDetail.ProjectDeadline.
			Deadline: tsString(j.StepDeadline),
			Steps:    steps[j.JobID],
		},
		SourceLanguage:  j.SourceLanguage,
		FileFormat:      string(j.FileFormat),
		ProjectDeadline: tsString(j.ProjectDeadline),
	}, nil
}

func (b *directBackend) GetJobLeverage(ctx context.Context, in GetJobIn) (Leverage, error) {
	// Tenant check first; the two leverage queries below take a bare jobID.
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return Leverage{}, err
	}
	bands, err := b.q.GetSavedAnalysisBands(ctx, j.JobID)
	if err != nil {
		return Leverage{}, fmt.Errorf("get leverage bands: %w", err)
	}
	needsReview, err := b.q.GetJobLeverageNeedsReview(ctx, j.JobID)
	if err != nil {
		return Leverage{}, fmt.Errorf("get leverage needs-review: %w", err)
	}

	// Collapse the (band, is_auto_approved) grouping the storage layer uses into
	// per-band totals — the auto-approved split isn't actionable for a model.
	totals := map[string]*LeverageBand{}
	order := make([]string, 0, len(bands))
	for _, row := range bands {
		agg, ok := totals[row.Band]
		if !ok {
			agg = &LeverageBand{Band: row.Band}
			totals[row.Band] = agg
			order = append(order, row.Band)
		}
		agg.Segments += row.Segments
		agg.Words += row.Words
	}
	out := make([]LeverageBand, 0, len(order))
	var totalSegments, totalWords int32
	for _, band := range order {
		out = append(out, *totals[band])
		totalSegments += totals[band].Segments
		totalWords += totals[band].Words
	}
	// GetJobLeverageNeedsReview returns one row per band; the model only needs
	// the total volume still awaiting review, so sum across bands.
	var needsReviewWords int32
	for _, row := range needsReview {
		needsReviewWords += row.Words
	}
	return Leverage{
		JobID:            j.JobID,
		Bands:            out,
		NeedsReviewWords: needsReviewWords,
		TotalSegments:    totalSegments,
		TotalWords:       totalWords,
	}, nil
}

// --- Segments ---------------------------------------------------------------

func (b *directBackend) ListSegments(ctx context.Context, in ListSegmentsIn) (SegmentPage, error) {
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return SegmentPage{}, err
	}
	tenantID, _ := scope(ctx)
	limit := clampLimit(in.Limit)

	total, err := b.q.CountJobSegmentsPage(ctx, db.CountJobSegmentsPageParams{
		TargetLanguage: j.TargetLanguage,
		FileID:         j.FileID,
		TenantID:       tenantID,
		Status:         in.Status,
		Origin:         in.Origin,
		Query:          in.Query,
	})
	if err != nil {
		return SegmentPage{}, fmt.Errorf("count segments: %w", err)
	}

	rows, err := b.q.ListJobSegmentsPage(ctx, db.ListJobSegmentsPageParams{
		TargetLanguage: j.TargetLanguage,
		FileID:         j.FileID,
		TenantID:       tenantID,
		Status:         in.Status,
		Origin:         in.Origin,
		Query:          in.Query,
		Cursor:         int32(in.Cursor),
		RowLimit:       int32(limit),
	})
	if err != nil {
		return SegmentPage{}, fmt.Errorf("list segments: %w", err)
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.TranslationID
	}
	previous, err := stepround.PreviousByTranslation(ctx, b.q, tenantID, ids)
	if err != nil {
		return SegmentPage{}, err
	}

	page := SegmentPage{Segments: make([]Segment, 0, len(rows)), Total: total}
	for _, r := range rows {
		seg := Segment{
			TranslationID: r.TranslationID,
			SegmentID:     r.SegmentID,
			Position:      r.Position,
			SourceText:    r.SourceText,
			TargetText:    ptr.Deref(r.TargetText),
			Status:        string(r.Status),
			TMMatch:       r.TmMatchPercent,
			UpdatedAt:     tsVersion(r.UpdatedAt),
		}
		if r.Origin.Valid {
			seg.Origin = string(r.Origin.TranslationOrigin)
		}
		if e, ok := previous[r.TranslationID]; ok {
			seg.PreviousStep = &StepOutcome{
				StepName: e.StepName, StepOrder: e.StepOrder, Round: e.Round, EndReason: e.EndReason,
				Status: e.Status, AIOutcome: e.AIOutcome, Edited: e.Edited,
			}
		}
		page.Segments = append(page.Segments, seg)
	}
	if n := len(page.Segments); n > 0 {
		// next_cursor is last position + 1 because the SQL comparison is
		// inclusive ("start at this position"). Returning the last position
		// itself would re-serve it as the first row of the next page.
		page.NextCursor = int64(page.Segments[n-1].Position) + 1
		page.HasMore = int64(n) < total
	}
	if page.HasMore {
		page.Hint = fmt.Sprintf(
			"showing %d of %d matching segments; pass cursor=%d for the next page, or narrow with status/origin/query",
			len(page.Segments), total, page.NextCursor)
	}
	return page, nil
}

// --- Translation memory + terminology ---------------------------------------

func (b *directBackend) SearchTM(ctx context.Context, in SearchTMIn) (TMHitList, error) {
	// Which TMs are in scope is a property of the project's settings profile, so
	// the tenant-scoped project fetch both authorises and resolves.
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return TMHitList{}, err
	}
	srcLang := langcode.Normalize(in.SourceLanguage)
	if srcLang == "" {
		srcLang = langcode.Normalize(p.SourceLanguage)
	}
	query := in.Query
	rows, err := b.q.SettingsConcordanceSearch(ctx, db.SettingsConcordanceSearchParams{
		SettingsProfileID: p.SettingsProfileID,
		SourceLanguage:    srcLang,
		TargetLanguage:    langcode.Normalize(in.TargetLanguage),
		Query:             &query,
		EntryLimit:        int32(clampLimit(in.Limit)),
	})
	if err != nil {
		return TMHitList{}, fmt.Errorf("search tm: %w", err)
	}
	out := make([]TMHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, TMHit{
			EntryID:        r.ID,
			SourceText:     r.SourceText,
			TargetText:     r.TargetText,
			SourceLanguage: r.SourceLanguage,
			TargetLanguage: r.TargetLanguage,
		})
	}
	return TMHitList{Matches: out, Count: len(out)}, nil
}

func (b *directBackend) LookupTerm(ctx context.Context, in LookupTermIn) (TermHitList, error) {
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return TermHitList{}, err
	}
	srcLang := in.SourceLanguage
	if srcLang == "" {
		srcLang = p.SourceLanguage
	}
	// Reuse the CAT editor's tokenizer rather than reimplementing it — a second
	// tokenizer would silently disagree with the editor's sidebar about which
	// terms apply to a segment.
	tokens := terminology.TokenizeSourceText(in.SourceText, 4)
	rows, err := b.q.LookupSettingsTermsByTokens(ctx, db.LookupSettingsTermsByTokensParams{
		SettingsProfileID: p.SettingsProfileID,
		SourceLanguage:    srcLang,
		TargetLanguage:    in.TargetLanguage,
		Tokens:            tokens,
	})
	if err != nil {
		return TermHitList{}, fmt.Errorf("lookup terms: %w", err)
	}
	out := make([]TermHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, TermHit{
			TermID:       r.ID,
			ConceptID:    r.ConceptID,
			SourceTerm:   r.SourceTerm,
			TargetTerm:   r.TargetTerm,
			Status:       r.Status,
			Notes:        r.Notes,
			Domain:       r.Domain,
			TermBaseName: r.TermBaseName,
		})
	}
	return TermHitList{Terms: out, Count: len(out)}, nil
}

// maxConceptMatches caps lookup_concept results. Exact-text matches are few, but a
// common word could appear across many concepts — the cap keeps a lookup from
// flooding the model's context; the hint tells it to narrow.
const maxConceptMatches = 50

// LookupConcept resolves concept_ids inside ONE base by exact term text (+ optional
// exact language/domain). Base-scoped and project-independent, unlike LookupTerm.
func (b *directBackend) LookupConcept(ctx context.Context, in LookupConceptIn) (ConceptMatchList, error) {
	tenantID, _ := scope(ctx)

	// Ownership: a foreign term_base_id yields the opaque not-found.
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: in.TermBaseID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConceptMatchList{}, errNotFound
		}
		return ConceptMatchList{}, fmt.Errorf("get term base: %w", err)
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return ConceptMatchList{}, invalidf("text is required")
	}
	lang := ""
	if in.Language != "" {
		lang = langcode.Normalize(in.Language)
	}
	filterDomain := in.Domain != nil
	domain := ""
	if in.Domain != nil {
		domain = strings.TrimSpace(*in.Domain)
	}

	rows, err := b.q.FindConceptsByTerm(ctx, db.FindConceptsByTermParams{
		TermBaseID:   base.ID,
		Text:         text,
		Language:     lang,
		FilterDomain: filterDomain,
		Domain:       domain,
		RowLimit:     maxConceptMatches + 1, // +1 to detect truncation
	})
	if err != nil {
		return ConceptMatchList{}, fmt.Errorf("find concepts: %w", err)
	}

	hint := ""
	if len(rows) > maxConceptMatches {
		rows = rows[:maxConceptMatches]
		hint = fmt.Sprintf("more than %d concepts match; narrow with language or domain", maxConceptMatches)
	}
	if len(rows) == 0 {
		return ConceptMatchList{Concepts: []ConceptMatch{}, Count: 0}, nil
	}

	// Fetch every matched concept's terms in one query so a model can tell
	// homographs apart (same text, different domain/meaning) before choosing an id.
	ids := make([]int64, len(rows))
	byID := make(map[int64]int, len(rows))
	concepts := make([]ConceptMatch, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		byID[r.ID] = i
		concepts[i] = ConceptMatch{ConceptID: r.ID, Definition: r.Definition, Domain: r.Domain, Terms: []ConceptMatchTerm{}}
	}
	terms, err := b.q.ListTermsForConcepts(ctx, ids)
	if err != nil {
		return ConceptMatchList{}, fmt.Errorf("list terms: %w", err)
	}
	for _, t := range terms {
		if i, ok := byID[t.ConceptID]; ok {
			concepts[i].Terms = append(concepts[i].Terms, ConceptMatchTerm{
				TermID: t.ID, Language: t.Language, Text: t.Text, Status: t.Status,
			})
		}
	}
	return ConceptMatchList{Concepts: concepts, Count: len(concepts), Hint: hint}, nil
}

// --- QA ---------------------------------------------------------------------

// maxQAIssues caps the issue list in a QA report. A job with 12,000 tag errors
// would otherwise flood the model's context; the summary counts stay complete.
const maxQAIssues = 100

func (b *directBackend) GetQAReport(ctx context.Context, in GetJobIn) (QAReport, error) {
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return QAReport{}, err
	}
	summary, err := b.qaSummary(ctx, j.FileID, j.TargetLanguage)
	if err != nil {
		return QAReport{}, err
	}
	// Unscoped by signature; runs on fileID + targetLanguage taken from the
	// tenant-scoped row above, never from args.
	results, err := b.q.ListQAResultsForJob(ctx, db.ListQAResultsForJobParams{
		FileID:         j.FileID,
		TargetLanguage: j.TargetLanguage,
	})
	if err != nil {
		return QAReport{}, fmt.Errorf("list qa results: %w", err)
	}

	report := QAReport{JobID: j.JobID, Summary: summary}
	report.HasMore = len(results) > maxQAIssues
	if report.HasMore {
		results = results[:maxQAIssues]
		report.Hint = fmt.Sprintf(
			"showing the first %d issues; summary counts above are complete", maxQAIssues)
	}
	report.Issues = make([]QAIssue, 0, len(results))
	for _, r := range results {
		report.Issues = append(report.Issues, QAIssue{
			TranslationID:   r.TranslationID,
			SegmentPosition: r.SegmentPosition,
			CheckType:       r.CheckType,
			Severity:        r.Severity,
			Message:         r.Message,
			Ignored:         r.Ignored,
			IgnoredBy:       ptr.Deref(r.IgnoredByName),
			IgnoredAt:       tsString(r.IgnoredAt),
		})
	}
	return report, nil
}

// qaSummary folds the per-bucket counts for one job. Unscoped by signature;
// callers pass ids from a tenant-scoped job row.
func (b *directBackend) qaSummary(ctx context.Context, fileID int64, targetLanguage string) (QASummary, error) {
	counts, err := b.q.CountQAIssuesByJob(ctx, db.CountQAIssuesByJobParams{
		FileID:         fileID,
		TargetLanguage: targetLanguage,
	})
	if err != nil {
		return QASummary{}, fmt.Errorf("count qa issues: %w", err)
	}
	var s QASummary
	for _, c := range counts {
		switch c.Bucket {
		case "error":
			s.Error = int(c.Count)
		case "warning":
			s.Warning = int(c.Count)
		case "info":
			s.Info = int(c.Count)
		case "ignored":
			s.Ignored = int(c.Count)
		}
	}
	return s, nil
}

// RunQA recomputes every finding of a job synchronously. Findings whose
// message is unchanged keep their ignore state (see qa.Service.RunAndPersistJob).
func (b *directBackend) RunQA(ctx context.Context, in GetJobIn) (RunQAResult, error) {
	j, err := b.job(ctx, in.JobID)
	if err != nil {
		return RunQAResult{}, err
	}
	tenantID, _ := scope(ctx)
	run, err := b.qaSvc.RunAndPersistJob(ctx, j.JobID, tenantID)
	if err != nil {
		if errors.Is(err, qa.ErrNoQAProfile) {
			return RunQAResult{}, invalidf(
				"this project has no QA profile assigned; assign one in project settings before running QA")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return RunQAResult{}, errNotFound
		}
		return RunQAResult{}, fmt.Errorf("run qa: %w", err)
	}
	summary, err := b.qaSummary(ctx, j.FileID, j.TargetLanguage)
	if err != nil {
		return RunQAResult{}, err
	}
	return RunQAResult{JobID: j.JobID, Findings: run.Findings, NoRules: run.NoRules, Summary: summary}, nil
}

// IgnoreQAFinding flips the ignore flag on one stored finding and returns the
// segment's findings afterwards.
func (b *directBackend) IgnoreQAFinding(ctx context.Context, in IgnoreQAFindingIn) (QAFindingList, error) {
	checkType := strings.TrimSpace(in.CheckType)
	if checkType == "" {
		return QAFindingList{}, invalidf("check_type is required; take it from openloc_get_qa_report")
	}
	if _, err := b.translation(ctx, in.TranslationID); err != nil {
		return QAFindingList{}, err
	}
	tenantID, userID := scope(ctx)
	rows, err := b.qaSvc.SetIgnored(ctx, qa.SetIgnoredParams{
		TranslationID: in.TranslationID,
		TenantID:      tenantID,
		UserID:        userID,
		CheckType:     checkType,
		Ignored:       !in.Unignore,
	})
	if err != nil {
		if errors.Is(err, qa.ErrNoQAFinding) {
			return QAFindingList{}, invalidf(
				"no current %q finding on this segment; run openloc_get_qa_report for the current list", checkType)
		}
		return QAFindingList{}, fmt.Errorf("set qa finding ignored: %w", err)
	}
	out := QAFindingList{TranslationID: in.TranslationID, Findings: make([]QAIssue, 0, len(rows))}
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

// --- Tasks ------------------------------------------------------------------

func (b *directBackend) ListMyTasks(ctx context.Context, in ListMyTasksIn) (TaskList, error) {
	tenantID, userID := scope(ctx)
	// Scoped by BOTH tenant and user: this is "my work", and a linguist must
	// never see another linguist's assignments through it.
	rows, err := b.q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{
		TenantID:   tenantID,
		AssignedTo: &userID,
		Lim:        int32(clampLimit(in.Limit)),
	})
	if err != nil {
		return TaskList{}, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TaskSummary, 0, len(rows))
	for _, t := range rows {
		out = append(out, TaskSummary{
			JobID:           t.ID,
			ProjectName:     t.ProjectName,
			StepName:        t.StepName,
			FileName:        t.FileName,
			TargetLanguage:  t.TargetLanguage,
			Status:          t.Status,
			WordCount:       t.WordCount,
			TranslatedCount: ptr.Deref(t.TranslatedCount),
			TotalCount:      ptr.Deref(t.TotalCount),
			Deadline:        tsString(t.Deadline),
		})
	}
	pending, err := b.q.ListMCPUpcomingSteps(ctx, db.ListMCPUpcomingStepsParams{
		TenantID: tenantID, UserID: userID, RowLimit: int32(clampLimit(in.Limit)),
	})
	if err != nil {
		return TaskList{}, fmt.Errorf("list upcoming steps: %w", err)
	}
	upcoming := make([]UpcomingTask, 0, len(pending))
	for _, u := range pending {
		upcoming = append(upcoming, UpcomingTask{
			JobID: u.JobID, ProjectName: u.ProjectName, FileName: u.FileName,
			TargetLanguage: u.TargetLanguage, StepName: u.StepName,
			CurrentStep: u.CurrentStep, Deadline: tsString(u.Deadline),
		})
	}
	return TaskList{Tasks: out, Count: len(out), Upcoming: upcoming}, nil
}
