package terminology

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// QuickAddInput is one source/target term pair added to a base in one step.
// Domain identifies the concept together with the source term; Definition
// only fills an empty concept definition.
type QuickAddInput struct {
	SourceTerm     string
	TargetTerm     string
	TargetLanguage string
	Status         string
	Definition     string
	Notes          string
	Domain         string
}

// QuickAddResult names the concept the pair landed in and the target term.
type QuickAddResult struct {
	ConceptID      int64  `json:"concept_id"`
	TermID         int64  `json:"term_id"`
	SourceTerm     string `json:"source_term"`
	TargetTerm     string `json:"target_term"`
	Status         string `json:"status"`
	CreatedConcept bool   `json:"created_concept"`
}

func invalidQuickAdd(format string, args ...any) error {
	return httputil.NewClientError(fmt.Sprintf(format, args...))
}

// QuickAddTerm adds a source/target pair to a base. A live concept with the
// same source term and domain is the same meaning, so the target term joins
// it; otherwise a new concept holds both terms. Adding the same pair twice
// returns the existing term. Status defaults to approved.
func (s *Service) QuickAddTerm(ctx context.Context, base db.TermBasis, in QuickAddInput, userID int64) (QuickAddResult, error) {
	src := strings.TrimSpace(in.SourceTerm)
	tgt := strings.TrimSpace(in.TargetTerm)
	if src == "" || tgt == "" {
		return QuickAddResult{}, invalidQuickAdd("source_term and target_term are required")
	}
	tgtLang := langcode.Normalize(in.TargetLanguage)
	if tgtLang == "" {
		return QuickAddResult{}, invalidQuickAdd("target_language is required")
	}
	if tgtLang == langcode.Normalize(base.SourceLanguage) {
		return QuickAddResult{}, invalidQuickAdd("target_language must differ from the base's source language")
	}
	status := in.Status
	if status == "" {
		status = "approved"
	}
	if !isValidStatus(status) {
		return QuickAddResult{}, invalidQuickAdd("status must be approved, pending, or forbidden")
	}
	domain := strings.TrimSpace(in.Domain)
	definition := strings.TrimSpace(in.Definition)
	tgtTerm := ConceptTermInput{Language: tgtLang, Text: tgt, Status: status, Notes: strings.TrimSpace(in.Notes)}

	res := QuickAddResult{SourceTerm: src, TargetTerm: tgt}
	err := s.inTx(ctx, func(q db.Querier) error {
		existing, err := q.FindConceptBySourceTerm(ctx, db.FindConceptBySourceTermParams{
			TermBaseID: base.ID, Domain: domain, Language: base.SourceLanguage, Text: src,
		})
		switch {
		case err == nil:
			res.ConceptID = existing.ID
			if definition != "" && strings.TrimSpace(existing.Definition) == "" {
				if err := q.UpdateTermConcept(ctx, db.UpdateTermConceptParams{
					ID: existing.ID, Definition: definition, Domain: existing.Domain, UpdatedBy: &userID,
				}); err != nil {
					return fmt.Errorf("set definition: %w", err)
				}
			}
		case errors.Is(err, pgx.ErrNoRows):
			id, err := CreateConcept(ctx, q, base, ConceptInput{
				Definition: definition,
				Domain:     domain,
				Terms:      []ConceptTermInput{{Language: base.SourceLanguage, Text: src, Status: "approved"}},
			}, &userID)
			if err != nil {
				return fmt.Errorf("create concept: %w", err)
			}
			res.ConceptID = id
			res.CreatedConcept = true
		default:
			return fmt.Errorf("find concept: %w", err)
		}
		term, err := AttachTerm(ctx, q, res.ConceptID, tgtTerm, userID)
		if err != nil {
			return err
		}
		res.TermID = term.ID
		res.Status = term.Status
		return nil
	})
	if err != nil {
		return QuickAddResult{}, err
	}
	s.syncBaseLanguages(ctx, base.ID)
	return res, nil
}

// AttachTerm adds a term to a concept idempotently: a live term with the same
// language and text is returned as is. Inputs must already be normalized.
func AttachTerm(ctx context.Context, q db.Querier, conceptID int64, t ConceptTermInput, userID int64) (db.Term, error) {
	if existing, err := q.FindConceptTerm(ctx, db.FindConceptTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text,
	}); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Term{}, fmt.Errorf("find term: %w", err)
	}
	created, err := q.CreateTerm(ctx, db.CreateTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text, Status: t.Status,
		PartOfSpeech: t.PartOfSpeech, Notes: t.Notes, CreatedBy: &userID, UpdatedBy: &userID,
	})
	if err != nil {
		return db.Term{}, fmt.Errorf("create term: %w", err)
	}
	return created, nil
}
