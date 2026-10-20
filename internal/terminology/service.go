package terminology

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
)

// Service is the terminology logic shared by the REST handlers and the MCP
// tools. Methods taking a db.TermBasis or a baseID expect the caller to have
// resolved the base with the tenant-scoped GetTermBase first.
type Service struct {
	queries db.Querier
	pool    *pgxpool.Pool // for transactions (concept + terms written atomically)
}

func NewService(q db.Querier, pool *pgxpool.Pool) *Service {
	return &Service{queries: q, pool: pool}
}

// inTx runs fn inside a transaction with a tx-scoped querier, so a projected
// entry write (concept + source term + target term) is all-or-nothing.
func (s *Service) inTx(ctx context.Context, fn func(q db.Querier) error) error {
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

// syncBaseLanguages recomputes a base's target_languages from its live terms.
// Best-effort: a failure here doesn't fail the mutation that triggered it.
func (s *Service) syncBaseLanguages(ctx context.Context, baseID int64) {
	if err := s.queries.SyncTermBaseTargetLanguages(ctx, baseID); err != nil {
		logging.Warn(ctx, err, "sync term base target languages", "termbase-sync-languages-failed",
			logging.TermbaseID(baseID))
	}
}

// BaseWithStats is a term base list row plus its concept count and the
// languages actually present (the concept model has no single pair).
type BaseWithStats struct {
	db.ListTermBasesRow
	EntryCount int64    `json:"entry_count"`
	Languages  []string `json:"languages"`
}

// ListBases lists the tenant's live term bases with their stats. Term bases
// are few, so the per-base fan-out is fine.
func (s *Service) ListBases(ctx context.Context, tenantID int64) ([]BaseWithStats, error) {
	bases, err := s.queries.ListTermBases(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list term bases: %w", err)
	}
	out := make([]BaseWithStats, len(bases))
	for i, b := range bases {
		count, err := s.queries.CountTermBaseEntries(ctx, b.ID)
		if err != nil {
			return nil, fmt.Errorf("count concepts: %w", err)
		}
		langs, err := s.queries.ListTermBaseLanguages(ctx, b.ID)
		if err != nil {
			return nil, fmt.Errorf("list languages: %w", err)
		}
		if langs == nil {
			langs = []string{}
		}
		out[i] = BaseWithStats{ListTermBasesRow: b, EntryCount: count, Languages: langs}
	}
	return out, nil
}

// DeleteBase soft-deletes a base and cascades to its concepts and terms in one
// transaction. Set-based UPDATEs; the rows stay for audit and restore.
func (s *Service) DeleteBase(ctx context.Context, baseID int64) error {
	return s.inTx(ctx, func(q db.Querier) error {
		if err := q.SoftDeleteTermBaseTerms(ctx, baseID); err != nil {
			return err
		}
		if err := q.SoftDeleteTermBaseConcepts(ctx, baseID); err != nil {
			return err
		}
		return q.SoftDeleteTermBase(ctx, baseID)
	})
}

// DeleteConcept soft-deletes a concept of the base; pgx.ErrNoRows when it is
// not in the base.
func (s *Service) DeleteConcept(ctx context.Context, baseID, conceptID, userID int64) error {
	if _, err := s.queries.GetConcept(ctx, db.GetConceptParams{ID: conceptID, TermBaseID: baseID}); err != nil {
		return err
	}
	if err := s.queries.SoftDeleteTermConcept(ctx, db.SoftDeleteTermConceptParams{ID: conceptID, UpdatedBy: &userID}); err != nil {
		return fmt.Errorf("soft-delete concept: %w", err)
	}
	s.syncBaseLanguages(ctx, baseID)
	return nil
}

// TermInConcept returns a live term of a concept of the base, or pgx.ErrNoRows.
func (s *Service) TermInConcept(ctx context.Context, baseID, conceptID, termID int64) (db.Term, error) {
	if _, err := s.queries.GetConcept(ctx, db.GetConceptParams{ID: conceptID, TermBaseID: baseID}); err != nil {
		return db.Term{}, err
	}
	term, err := s.queries.GetTerm(ctx, termID)
	if err != nil {
		return db.Term{}, err
	}
	if term.ConceptID != conceptID || term.DeletedAt.Valid {
		return db.Term{}, pgx.ErrNoRows
	}
	return term, nil
}

// DeleteTerm soft-deletes a term the caller resolved with TermInConcept.
func (s *Service) DeleteTerm(ctx context.Context, baseID, termID, userID int64) error {
	if err := s.queries.SoftDeleteTerm(ctx, db.SoftDeleteTermParams{ID: termID, UpdatedBy: &userID}); err != nil {
		return fmt.Errorf("soft-delete term: %w", err)
	}
	s.syncBaseLanguages(ctx, baseID)
	return nil
}

// ConceptFilter narrows a concept listing. Search matches domain, definition
// or any term text; Langs keeps concepts with a term in any of the languages
// and narrows each concept's terms to them. Limit is clamped to 1–500
// (default 100). SkipTotal leaves ConceptPage.Total at 0 and saves the
// count query, for callers that never show a total.
type ConceptFilter struct {
	Search    string
	Langs     []string
	Cursor    int64
	Limit     int32
	SkipTotal bool
}

// ConceptPage is one keyset page of concepts with their terms.
type ConceptPage struct {
	Concepts   []ConceptDTO
	NextCursor int64
	HasMore    bool
	Total      int64
}

func (s *Service) ListConcepts(ctx context.Context, baseID int64, f ConceptFilter) (ConceptPage, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var langs []string
	langSet := make(map[string]bool)
	for _, l := range f.Langs {
		if l == "" {
			continue
		}
		n := langcode.Normalize(l)
		if !langSet[n] {
			langSet[n] = true
			langs = append(langs, n)
		}
	}

	var total int64
	if !f.SkipTotal {
		n, err := s.queries.CountConcepts(ctx, db.CountConceptsParams{TermBaseID: baseID, Search: f.Search, Langs: langs})
		if err != nil {
			return ConceptPage{}, fmt.Errorf("count concepts: %w", err)
		}
		total = n
	}
	rows, err := s.queries.ListConceptsPage(ctx, db.ListConceptsPageParams{
		TermBaseID: baseID, Cursor: f.Cursor, Search: f.Search, Langs: langs, PageLimit: f.Limit,
	})
	if err != nil {
		return ConceptPage{}, fmt.Errorf("list concepts: %w", err)
	}

	concepts := make([]ConceptDTO, len(rows))
	ids := make([]int64, len(rows))
	byID := make(map[int64]*ConceptDTO, len(rows))
	for i, c := range rows {
		concepts[i] = ConceptDTO{
			ID: c.ID, Definition: c.Definition, Domain: c.Domain,
			CreatedAt: tsString(c.CreatedAt), UpdatedAt: tsString(c.UpdatedAt),
			CreatedBy: c.CreatedByName, UpdatedBy: c.UpdatedByName,
			Terms: []ConceptTermDTO{},
		}
		ids[i] = c.ID
		byID[c.ID] = &concepts[i]
	}
	if len(ids) > 0 {
		terms, err := s.queries.ListTermsForConcepts(ctx, ids)
		if err != nil {
			return ConceptPage{}, fmt.Errorf("list terms: %w", err)
		}
		for _, t := range terms {
			if len(langSet) > 0 && !langSet[t.Language] {
				continue
			}
			if c := byID[t.ConceptID]; c != nil {
				c.Terms = append(c.Terms, termRowDTO(t))
			}
		}
	}

	page := ConceptPage{Concepts: concepts, HasMore: len(rows) == int(f.Limit), Total: total}
	if len(rows) > 0 {
		page.NextCursor = rows[len(rows)-1].ID
	}
	return page, nil
}

// GetConcept returns one live concept of the base with all its terms, or
// pgx.ErrNoRows.
func (s *Service) GetConcept(ctx context.Context, baseID, conceptID int64) (ConceptDTO, error) {
	return loadConcept(ctx, s.queries, baseID, conceptID)
}

func loadConcept(ctx context.Context, q db.Querier, baseID, conceptID int64) (ConceptDTO, error) {
	concept, err := q.GetConcept(ctx, db.GetConceptParams{ID: conceptID, TermBaseID: baseID})
	if err != nil {
		return ConceptDTO{}, err
	}
	terms, err := q.ListTermsForConcepts(ctx, []int64{conceptID})
	if err != nil {
		return ConceptDTO{}, fmt.Errorf("list terms: %w", err)
	}
	dto := ConceptDTO{
		ID: concept.ID, Definition: concept.Definition, Domain: concept.Domain,
		CreatedAt: tsString(concept.CreatedAt), UpdatedAt: tsString(concept.UpdatedAt),
		Terms: []ConceptTermDTO{},
	}
	for _, t := range terms {
		dto.Terms = append(dto.Terms, termRowDTO(t))
	}
	return dto, nil
}

// CreateBase creates an empty term base. Its target languages are derived
// from the terms added later. Caller mistakes come back as ClientError.
func (s *Service) CreateBase(ctx context.Context, tenantID, userID int64, name, description, sourceLanguage string) (db.TermBasis, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.TermBasis{}, httputil.NewClientError("name is required")
	}
	if strings.TrimSpace(sourceLanguage) == "" {
		return db.TermBasis{}, httputil.NewClientError("source_language is required")
	}
	base, err := s.queries.CreateTermBase(ctx, db.CreateTermBaseParams{
		TenantID:       tenantID,
		Name:           name,
		Description:    description,
		SourceLanguage: langcode.Normalize(sourceLanguage),
		CreatedBy:      auth.UserIDPtr(userID),
	})
	if err != nil {
		return db.TermBasis{}, fmt.Errorf("create term base: %w", err)
	}
	return base, nil
}

// AddConcept creates a concept with its terms and returns it as stored, read
// back in the same transaction, so a failed read saves nothing. It then
// refreshes the base's target languages. Terms are normalized and
// de-duplicated by CreateConcept.
func (s *Service) AddConcept(ctx context.Context, base db.TermBasis, in ConceptInput, userID int64) (ConceptDTO, error) {
	var c ConceptDTO
	err := s.inTx(ctx, func(q db.Querier) error {
		id, err := CreateConcept(ctx, q, base, in, &userID)
		if err != nil {
			return err
		}
		c, err = loadConcept(ctx, q, base.ID, id)
		return err
	})
	if err != nil {
		return ConceptDTO{}, fmt.Errorf("create concept: %w", err)
	}
	s.syncBaseLanguages(ctx, base.ID)
	return c, nil
}
