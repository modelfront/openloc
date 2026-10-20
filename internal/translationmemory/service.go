package translationmemory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/logging"
)

// Service is the TM logic shared by the REST handlers and the MCP tools.
// Callers resolve the TM with a tenant-scoped GetTMCollection first; methods
// that take a tenantID also re-check it in SQL.
type Service struct {
	queries       db.Querier
	db            db.DBTX       // raw DB for the multi-row INSERT in TMX import
	pool          *pgxpool.Pool // for transactions (UpdateEntry: history + update atomic)
	countTimeout  time.Duration // bounds the match count of an entry search
	searchTimeout time.Duration // bounds the page query of an entry search
}

func NewService(q db.Querier, dbtx db.DBTX, pool *pgxpool.Pool) *Service {
	return &Service{queries: q, db: dbtx, pool: pool, countTimeout: defaultSearchTimeout, searchTimeout: defaultSearchTimeout}
}

// ValidationError is a caller mistake; Error() is safe to show the user.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// ErrConflict means the entry changed since the caller read it (optimistic lock).
var ErrConflict = errors.New("TM entry was updated by another user")

// InUseError refuses a TM delete while settings rows still point at it.
type InUseError struct{ Assignments int32 }

func (e InUseError) Error() string {
	return fmt.Sprintf("cannot delete: %d assignment(s) (projects, templates or TM profiles) use this TM", e.Assignments)
}

// CreateCollectionInput mirrors the create form: a source language, one or
// more target languages, and an optional customer (nil or 0 = none).
type CreateCollectionInput struct {
	Name            string
	SourceLanguage  string
	TargetLanguages []string
	CustomerID      *int64
}

func (s *Service) CreateCollection(ctx context.Context, tenantID, userID int64, in CreateCollectionInput) (db.TmCollection, error) {
	src := langcode.Normalize(in.SourceLanguage)
	targets := langcode.NormalizeAll(in.TargetLanguages)
	if in.Name == "" || src == "" || len(targets) == 0 {
		return db.TmCollection{}, invalid("name, source_language, and target_languages are required")
	}
	seen := make(map[string]bool, len(targets))
	for _, tgt := range targets {
		if tgt == src {
			return db.TmCollection{}, invalid("target language %q cannot be the same as source language", tgt)
		}
		if seen[tgt] {
			return db.TmCollection{}, invalid("duplicate target language %q", tgt)
		}
		seen[tgt] = true
	}

	var customerID *int64
	if in.CustomerID != nil && *in.CustomerID != 0 {
		if _, err := s.queries.GetCustomer(ctx, db.GetCustomerParams{ID: *in.CustomerID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.TmCollection{}, invalid("customer not found in this tenant")
			}
			return db.TmCollection{}, fmt.Errorf("verify customer: %w", err)
		}
		customerID = in.CustomerID
	}

	tm, err := s.queries.CreateTMCollection(ctx, db.CreateTMCollectionParams{
		Name:            in.Name,
		TenantID:        tenantID,
		SourceLanguage:  &src,
		TargetLanguages: targets,
		CustomerID:      customerID,
		CreatedBy:       auth.UserIDPtr(userID),
	})
	if err != nil {
		return db.TmCollection{}, fmt.Errorf("create TM: %w", err)
	}
	return tm, nil
}

// DeleteCollection soft-deletes a TM. Refused with
// InUseError while any project, template or TM profile uses it; pgx.ErrNoRows
// when the TM is not in the tenant.
func (s *Service) DeleteCollection(ctx context.Context, tenantID, tmID int64) error {
	refs, err := s.queries.CountTMReferences(ctx, tmID)
	if err != nil {
		return fmt.Errorf("count TM references: %w", err)
	}
	if refs > 0 {
		return InUseError{Assignments: refs}
	}
	n, err := s.queries.SoftDeleteTMCollection(ctx, db.SoftDeleteTMCollectionParams{ID: tmID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("soft-delete TM: %w", err)
	}
	if n == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// EntryFilter narrows a TM entry listing. Query "" lists; otherwise it
// searches the fields Scope names (ScopeBoth by default): a case-insensitive
// substring match, unless Exact (the whole field equals Query), CaseSensitive
// or Regex (Query is a regular expression) say otherwise. Limit is clamped to
// 1–500 (default 100). SkipTotal saves the count query and leaves
// EntryPage.Total at UnknownTotal, for callers that never show a total.
type EntryFilter struct {
	SourceLanguage string
	TargetLanguage string
	Query          string
	Scope          string
	Exact          bool
	CaseSensitive  bool
	Regex          bool
	Cursor         int64
	Limit          int32
	SkipTotal      bool
}

// UnknownTotal is EntryPage.Total when a search did not count its matches: on
// a page after the first, or when the count ran past its time limit.
const UnknownTotal int64 = -1

// EntryPage is one keyset page. Entries is filled when listing, Matches when
// searching (the search rows carry a similarity score).
type EntryPage struct {
	Entries    []db.TranslationMemory
	Matches    []db.SearchTMEntriesCursorRow
	NextCursor int64
	HasMore    bool
	Total      int64
}

// ListEntries pages through one TM. The caller has already verified the TM
// belongs to the tenant. A search counts its matches on the first page only,
// and a count past countTimeout leaves the total unknown rather than failing
// the search. A bad Scope, a query over maxSearchRunes, a regex Postgres
// rejects and a page query past searchTimeout are ValidationErrors.
func (s *Service) ListEntries(ctx context.Context, tmID int64, f EntryFilter) (EntryPage, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	src := langcode.Normalize(f.SourceLanguage)
	tgt := langcode.Normalize(f.TargetLanguage)

	if f.Query != "" {
		inSource, inTarget, err := searchScope(f.Scope)
		if err != nil {
			return EntryPage{}, err
		}
		if err := checkSearchLength(f.Query); err != nil {
			return EntryPage{}, err
		}
		pattern := searchPattern(f)
		total := UnknownTotal
		if f.Cursor == 0 && !f.SkipTotal {
			if total, err = s.countSearch(ctx, db.CountTMEntriesSearchParams{
				TmID: tmID, SourceLanguage: src, TargetLanguage: tgt,
				InSource: inSource, InTarget: inTarget, CaseSensitive: f.CaseSensitive, Pattern: pattern,
			}); err != nil {
				return EntryPage{}, err
			}
		}
		searchCtx, cancel := context.WithTimeout(ctx, s.searchTimeout)
		defer cancel()
		rows, err := s.queries.SearchTMEntriesCursor(searchCtx, db.SearchTMEntriesCursorParams{
			TmID: tmID, SourceLanguage: src, TargetLanguage: tgt, Query: f.Query,
			InSource: inSource, InTarget: inTarget, CaseSensitive: f.CaseSensitive, Pattern: pattern,
			Cursor: f.Cursor, EntryLimit: f.Limit,
		})
		if err != nil {
			return EntryPage{}, fmt.Errorf("search TM entries: %w", searchError(ctx, searchCtx, err))
		}
		page := EntryPage{Matches: rows, HasMore: len(rows) == int(f.Limit), Total: total}
		if len(rows) > 0 {
			page.NextCursor = rows[len(rows)-1].ID
		}
		return page, nil
	}

	total := UnknownTotal
	if !f.SkipTotal {
		n, err := s.queries.CountTMEntries(ctx, db.CountTMEntriesParams{
			TmID: tmID, SourceLanguage: src, TargetLanguage: tgt,
		})
		if err != nil {
			return EntryPage{}, fmt.Errorf("count TM entries: %w", err)
		}
		total = n
	}
	rows, err := s.queries.ListTMEntriesCursor(ctx, db.ListTMEntriesCursorParams{
		TmID: tmID, SourceLanguage: src, TargetLanguage: tgt, Cursor: f.Cursor, EntryLimit: f.Limit,
	})
	if err != nil {
		return EntryPage{}, fmt.Errorf("list TM entries: %w", err)
	}
	page := EntryPage{Entries: rows, HasMore: len(rows) == int(f.Limit), Total: total}
	if len(rows) > 0 {
		page.NextCursor = rows[len(rows)-1].ID
	}
	return page, nil
}

// UpdateEntryInput is an inline edit of one entry. Empty rich text stores
// NULL. ExpectedUpdatedAt nil skips the version check.
type UpdateEntryInput struct {
	SourceText        string
	TargetText        string
	SourceRichText    string
	TargetRichText    string
	ExpectedUpdatedAt *time.Time
	ChangeReason      string
}

// UpdateEntry snapshots the entry into its history and updates it in one
// transaction. Errors: pgx.ErrNoRows (entry not in this TM and tenant),
// ValidationError, ErrConflict (stale ExpectedUpdatedAt).
func (s *Service) UpdateEntry(ctx context.Context, tenantID, userID, tmID, entryID int64, in UpdateEntryInput) (db.TranslationMemory, error) {
	before, err := s.queries.GetTMEntryScoped(ctx, db.GetTMEntryScopedParams{ID: entryID, TmID: tmID, TenantID: tenantID})
	if err != nil {
		return db.TranslationMemory{}, err
	}
	if in.SourceText == "" || in.TargetText == "" {
		return db.TranslationMemory{}, invalid("source_text and target_text are required")
	}
	user, err := s.queries.GetUser(ctx, userID)
	if err != nil {
		return db.TranslationMemory{}, fmt.Errorf("load user: %w", err)
	}
	// The same "Name <email>" label the pipeline write-back stamps.
	updatedByLabel := auth.UserLabel(user.Name, user.Email)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.TranslationMemory{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	if err := q.InsertTMHistory(ctx, db.InsertTMHistoryParams{
		EntryID:         entryID,
		SourceText:      before.SourceText,
		TargetText:      before.TargetText,
		SourceRichText:  before.SourceRichText,
		TargetRichText:  before.TargetRichText,
		ChangedByUserID: &userID,
		ChangeReason:    in.ChangeReason,
	}); err != nil {
		return db.TranslationMemory{}, fmt.Errorf("record history: %w", err)
	}

	updated, err := q.UpdateTMEntry(ctx, db.UpdateTMEntryParams{
		ID:                entryID,
		TmID:              tmID,
		SourceText:        in.SourceText,
		TargetText:        in.TargetText,
		SourceRichText:    in.SourceRichText,
		TargetRichText:    in.TargetRichText,
		UpdatedBy:         updatedByLabel,
		ExpectedUpdatedAt: expectedUpdatedAtArg(in.ExpectedUpdatedAt),
	})
	if err != nil {
		// Release the tx connection before the pool-bound recheck below: with
		// a small pool, concurrent stale writers holding one connection each
		// while waiting for a second one wedge the pool. The deferred
		// Rollback is then a no-op.
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			logging.Warn(ctx, rbErr, "rollback after failed TM entry update", "tm-entry-update-rollback-failed")
		}
		// ErrNoRows is a stale version or a row deleted since the read.
		if errors.Is(err, pgx.ErrNoRows) && in.ExpectedUpdatedAt != nil {
			if _, recheckErr := s.queries.GetTMEntryScoped(ctx, db.GetTMEntryScopedParams{
				ID: entryID, TmID: tmID, TenantID: tenantID,
			}); recheckErr == nil {
				return db.TranslationMemory{}, ErrConflict
			}
		}
		return db.TranslationMemory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.TranslationMemory{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}
