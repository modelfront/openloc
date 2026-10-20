package qa

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// Service exposes QA computation to other packages without leaking the
// HTTP handler abstractions. The translation handler uses it to enforce QA
// blocking on segment confirm; the QA handler and the MCP backend
// use it to run and persist QA for one segment or a whole job.
//
// The service holds a `Querier` so it can load profile rules, terms, and
// sibling targets just-in-time. The optional pool is only used to wrap each
// segment's upsert+prune in a transaction; without it (unit tests, the
// confirm-gate-only wiring) writes run on the plain querier.
type Service struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

// NewService constructs a QA service backed by the given querier.
func NewService(q db.Querier) *Service {
	return &Service{queries: q}
}

// NewServiceWithPool is NewService plus transaction support for the persisting
// paths (RunAndPersistTranslation, RunAndPersistJob). Production wiring uses this.
func NewServiceWithPool(q db.Querier, pool *pgxpool.Pool) *Service {
	return &Service{queries: q, pool: pool}
}

// ErrNoQAProfile is returned by RunAndPersistJob when the job's project has no
// QA profile assigned. Callers map it to a 400 / model-visible error.
var ErrNoQAProfile = errors.New("project has no QA profile assigned")

// inTx runs fn with a transaction-scoped querier when a pool is available, and
// on the plain querier otherwise.
func (s *Service) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	if s.pool == nil {
		return fn(s.queries)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CheckProposalParams describes a proposed translation to be QA-checked.
// `TranslationID` resolves the segment + project + profile chain server-
// side; the service does NOT trust client-supplied source/profile/lang.
// `ProposedText` and `ProposedRichText` are the new values the caller is
// about to write — the service runs checks against THESE, not the
// currently-stored target.
//
// TenantID is required: the resolution chain (translation → segment →
// file → project) is tenant-scoped to prevent a client from triggering
// QA against another tenant's project metadata.
type CheckProposalParams struct {
	TranslationID    int64
	TenantID         int64
	ProposedText     string
	ProposedRichText string // optional; empty = no XLIFF inline tags
}

// CheckResult is what the service returns. `Issues` is empty when the
// proposed translation passes all enabled rules. `ProfileAssigned` lets
// the caller distinguish "no errors" (rules ran, nothing wrong) from
// "no enforcement" (no profile, can't run anything) — both produce empty
// Issues, but the policy difference matters for blocking decisions.
type CheckResult struct {
	Issues          []Issue
	ProfileAssigned bool
}

// HasErrors returns true if any non-ignored issue has severity="error". The
// string match is intentional — severities are stored as plain TEXT (no enum)
// by design. Callers should use this helper instead of inlining the string
// comparison so a future severity-enum migration touches one place.
func (r CheckResult) HasErrors() bool {
	for _, issue := range r.Issues {
		if issue.Severity == "error" && !issue.Ignored {
			return true
		}
	}
	return false
}

// ConceptsFromLookupRows groups flat terminology lookup rows (each carrying
// concept_id + the target term's status) into per-concept QA units — the one
// place flat rows become concepts.
func ConceptsFromLookupRows(rows []db.LookupSettingsTermsRow) []ConceptForQA {
	trs := make([]TermRow, len(rows))
	for i, r := range rows {
		trs[i] = TermRow{
			ConceptID:    r.ConceptID,
			Domain:       r.Domain,
			SourceTerm:   r.SourceTerm,
			TargetTerm:   r.TargetTerm,
			Status:       r.Status,
			SourceStatus: r.SourceStatus,
		}
	}
	return GroupConcepts(trs)
}

// ruleSet is the enabled subset of a QA profile: check_type → severity, plus
// the per-check JSON config where one is set.
type ruleSet struct {
	severity map[string]string
	configs  map[string][]byte
}

func (rs ruleSet) empty() bool { return len(rs.severity) == 0 }

func (rs ruleSet) enabled(checkType string) bool {
	_, ok := rs.severity[checkType]
	return ok
}

func (s *Service) loadRules(ctx context.Context, qaProfileID int64) (ruleSet, error) {
	rules, err := s.queries.ListQARules(ctx, qaProfileID)
	if err != nil {
		return ruleSet{}, fmt.Errorf("list QA rules: %w", err)
	}
	rs := ruleSet{
		severity: make(map[string]string, len(rules)),
		configs:  make(map[string][]byte),
	}
	for _, rule := range rules {
		if rule.Enabled {
			rs.severity[rule.CheckType] = rule.Severity
			if len(rule.Config) > 2 { // skip empty "{}"
				rs.configs[rule.CheckType] = rule.Config
			}
		}
	}
	return rs, nil
}

// segmentScope is the per-segment context the checks need beyond the text
// itself: the project and file the segment lives in, and its languages.
type segmentScope struct {
	fileID            int64
	settingsProfileID int64
	sourceLanguage    string
	targetLanguage    string
	projectDomain     string
}

// checkSegment runs the pure checks for one segment. The glossary concepts
// (when terminology or forbidden_terms is enabled — either rule needs them)
// and the sibling targets for the consistency check come from lk.
func (s *Service) checkSegment(ctx context.Context, rs ruleSet, lk segmentLookups, translationID int64, source, target, sourceRich, targetRich string) []Issue {
	var concepts []ConceptForQA
	if rs.enabled("terminology") || rs.enabled("forbidden_terms") {
		concepts = lk.concepts(ctx, source)
	}

	issues := CheckAllWithParams(ctx, CheckAllParams{
		Source:         source,
		Target:         target,
		SourceRichText: sourceRich,
		TargetRichText: targetRich,
		Rules:          rs.severity,
		RuleConfigs:    rs.configs,
		Concepts:       concepts,
	})

	if sev, ok := rs.severity["consistency"]; ok {
		if issue := CheckConsistency(target, lk.otherTargets(ctx, translationID, source), sev); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// CheckTranslationProposal runs QA against a proposed human translation
// without persisting any results. Used by HandleUpdateHuman to decide
// whether to accept or reject a confirm request, and by
// RunAndPersistTranslation as the compute step.
//
// Resolution chain (all tenant-scoped via the JOIN through projects):
//
//	translation → segment → file → project → settings_profile → qa_profile
//
// If any link is missing OR cross-tenant, returns error (caller maps to
// 404). If profile is unassigned, returns CheckResult with
// ProfileAssigned=false and empty Issues (caller decides whether to
// gracefully allow or hard-fail).
//
// Issues that match a stored, ignored finding (same check_type + message)
// come back with Ignored=true, so the confirm gate does not block on them.
func (s *Service) CheckTranslationProposal(ctx context.Context, params CheckProposalParams) (CheckResult, error) {
	// Step 1: resolve the chain (tenant-scoped). GetTranslationTenant
	// proves the caller can see the translation; the rest follow by FK.
	//
	// The unscoped `Get*` calls below are SAFE here ONLY because the entry
	// guard above proved the translation belongs to params.TenantID; the
	// FK chain (translations.segment_id → segments.file_id → files.project_id
	// → projects.tenant_id) cannot cross tenants without violating the
	// schema. Each call carries an inline "safe:" rationale so a line copied
	// to a context without the entry guard is visibly unsafe.
	tr, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
		ID:       params.TranslationID,
		TenantID: params.TenantID,
	})
	if err != nil {
		return CheckResult{}, fmt.Errorf("get translation: %w", err)
	}

	// safe: tr.SegmentID is FK-bound to translations row owned by params.TenantID.
	seg, err := s.queries.GetSegment(ctx, tr.SegmentID)
	if err != nil {
		return CheckResult{}, fmt.Errorf("get segment: %w", err)
	}

	// safe: seg.FileID is FK-bound through the validated tr → seg chain.
	file, err := s.queries.GetFile(ctx, seg.FileID)
	if err != nil {
		return CheckResult{}, fmt.Errorf("get file: %w", err)
	}

	// safe: file.ProjectID is FK-bound through the validated chain above.
	project, err := s.queries.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		return CheckResult{}, fmt.Errorf("get project: %w", err)
	}

	// safe: project.SettingsProfileID is FK-bound through the validated chain.
	profile, err := s.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return CheckResult{}, fmt.Errorf("get settings profile: %w", err)
	}

	// Step 2: graceful degradation — no profile means no enforcement.
	// The caller (HandleUpdateHuman) translates this to "allow the save."
	if profile.QaProfileID == nil {
		return CheckResult{ProfileAssigned: false}, nil
	}

	// Step 3: load enabled rules. Same loader as the run-job path so stale
	// configs (rules disabled mid-edit) are honored consistently.
	rs, err := s.loadRules(ctx, *profile.QaProfileID)
	if err != nil {
		return CheckResult{}, err
	}

	// No enabled rules → empty issues but profile IS assigned (different
	// from "no profile"). Caller's behavior is identical in this branch
	// but the distinction matters for diagnostics + future policy.
	if rs.empty() {
		return CheckResult{ProfileAssigned: true}, nil
	}

	// Step 4: compute. The proposed text is what's being checked, NOT
	// the stored target (which may still hold the pre-edit value during
	// this transactional moment). Source comes from the segment record
	// (server-trusted) so a client can't fabricate a "clean source"
	// that hides errors.
	issues := s.checkSegment(ctx, rs, dbLookups{s: s, sc: segmentScope{
		fileID:            file.ID,
		settingsProfileID: project.SettingsProfileID,
		sourceLanguage:    project.SourceLanguage,
		targetLanguage:    tr.TargetLanguage,
		projectDomain:     ptr.Deref(project.Domain),
	}}, tr.ID, seg.SourceText, params.ProposedText, ptr.Deref(seg.SourceRichText), params.ProposedRichText)

	// Step 5: carry over ignore state from the stored findings.
	stored, err := s.queries.ListQAResultsForTranslation(ctx, tr.ID)
	if err != nil {
		return CheckResult{}, fmt.Errorf("list stored QA results: %w", err)
	}
	issues = markIgnored(issues, stored)

	return CheckResult{Issues: issues, ProfileAssigned: true}, nil
}

// persistIssues writes one segment's findings: upsert every issue (the SQL
// keeps the ignore flag while the message is unchanged), then prune the rows
// for checks that did not fire. Upsert-then-prune, never delete-then-insert —
// a leading delete would wipe the per-finding ignore state.
func persistIssues(ctx context.Context, q db.Querier, translationID int64, issues []Issue) error {
	keep := make([]string, 0, len(issues))
	for _, issue := range issues {
		if err := q.UpsertQAResult(ctx, db.UpsertQAResultParams{
			TranslationID: translationID,
			CheckType:     issue.CheckType,
			Severity:      issue.Severity,
			Message:       issue.Message,
		}); err != nil {
			return fmt.Errorf("persist QA result: %w", err)
		}
		keep = append(keep, issue.CheckType)
	}
	if err := q.PruneQAResultsForTranslation(ctx, db.PruneQAResultsForTranslationParams{
		TranslationID:  translationID,
		KeepCheckTypes: keep,
	}); err != nil {
		return fmt.Errorf("prune QA results: %w", err)
	}
	return nil
}

// RunAndPersistTranslation re-runs QA for a single translation against its
// CURRENTLY-STORED target text and persists the fresh results. This is the
// on-save refresh path: the CAT editor calls it after every
// autosave so the QA badges for the edited segment stay in sync without a
// manual full-job run.
//
// It reuses CheckTranslationProposal for the tenant-scoped resolution + compute
// (feeding it the stored active_text as the "proposal") so there is exactly one
// per-translation check implementation.
func (s *Service) RunAndPersistTranslation(ctx context.Context, translationID, tenantID int64) ([]Issue, error) {
	tr, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
		ID:       translationID,
		TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("get translation: %w", err)
	}

	result, err := s.CheckTranslationProposal(ctx, CheckProposalParams{
		TranslationID:    translationID,
		TenantID:         tenantID,
		ProposedText:     ptr.Deref(tr.ActiveText),
		ProposedRichText: ptr.Deref(tr.ActiveRichText),
	})
	if err != nil {
		return nil, err
	}

	if err := s.inTx(ctx, func(q db.Querier) error {
		return persistIssues(ctx, q, translationID, result.Issues)
	}); err != nil {
		return nil, err
	}
	return result.Issues, nil
}

// JobRunResult reports what a job-wide run did.
type JobRunResult struct {
	// Findings is the number of qa_results rows written, ignored ones included.
	Findings int
	// NoRules is true when the QA profile has no enabled rule; the run then
	// clears every finding of the job and writes nothing.
	NoRules bool
}

// RunAndPersistJob runs every enabled check on every segment of a job and
// persists the findings. Shared by POST /api/jobs/{id}/run-qa and the MCP
// openloc_run_qa tool.
//
// Tenant isolation: GetJobTenant is the guard; the follow-up reads run on ids
// it returned, never on the caller's job id directly. Returns ErrNoQAProfile
// when the project has no QA profile.
func (s *Service) RunAndPersistJob(ctx context.Context, jobID, tenantID int64) (JobRunResult, error) {
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return JobRunResult{}, fmt.Errorf("get job: %w", err)
	}

	js, err := s.resolveJobScope(ctx, job, tenantID)
	if err != nil {
		return JobRunResult{}, err
	}
	if !js.profileAssigned {
		return JobRunResult{}, ErrNoQAProfile
	}
	rs := js.rs

	// With no enabled rules there are no findings to keep, so a full clear is
	// the honest state — otherwise the previous run's issues would outlive the
	// rules that produced them.
	if rs.empty() {
		if err := s.queries.DeleteQAResultsForJob(ctx, db.DeleteQAResultsForJobParams{
			FileID:         job.FileID,
			TargetLanguage: job.TargetLanguage,
		}); err != nil {
			return JobRunResult{}, fmt.Errorf("clear QA results: %w", err)
		}
		return JobRunResult{NoRules: true}, nil
	}

	return s.runJobPages(ctx, job, js)
}
