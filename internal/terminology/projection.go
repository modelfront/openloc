package terminology

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
)

// ConceptInput is a full multilingual concept for the concept-native write path
// (TBX/CSV import + browser CRUD): any languages, synonyms, per-term status.
type ConceptInput struct {
	Definition string
	Domain     string
	Terms      []ConceptTermInput
}

// ConceptTermInput is one term of a concept, in one language.
type ConceptTermInput struct {
	Language     string
	Text         string
	Status       string
	PartOfSpeech string
	Notes        string
}

// DedupeConceptTerms drops any term that repeats an earlier (language, text)
// within the same concept — a concept never holds two identical terms in a
// language (domain is concept-level, so it's implicit). Languages are normalized
// for the comparison; the first occurrence wins. Input is not mutated.
func DedupeConceptTerms(terms []ConceptTermInput) []ConceptTermInput {
	seen := make(map[string]bool, len(terms))
	out := make([]ConceptTermInput, 0, len(terms))
	for _, t := range terms {
		key := langcode.Normalize(t.Language) + "\x00" + t.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

// CreateConcept inserts a concept plus all its terms (any languages, synonyms,
// per-term status) in the caller's transaction and returns the new concept's id.
// Languages are normalized; invalid statuses fall back to approved; duplicate
// terms are dropped.
func CreateConcept(ctx context.Context, q db.Querier, base db.TermBasis, c ConceptInput, userID *int64) (int64, error) {
	concept, err := q.CreateTermConcept(ctx, db.CreateTermConceptParams{
		TermBaseID: base.ID,
		Definition: c.Definition,
		Domain:     c.Domain,
		CreatedBy:  userID,
		UpdatedBy:  userID,
	})
	if err != nil {
		return 0, err
	}
	for _, t := range DedupeConceptTerms(c.Terms) {
		status := t.Status
		if !isValidStatus(status) {
			status = "approved"
		}
		if _, err := q.CreateTerm(ctx, db.CreateTermParams{
			ConceptID:    concept.ID,
			Language:     langcode.Normalize(t.Language),
			Text:         t.Text,
			Status:       status,
			PartOfSpeech: t.PartOfSpeech,
			Notes:        t.Notes,
			CreatedBy:    userID,
			UpdatedBy:    userID,
		}); err != nil {
			return 0, err
		}
	}
	return concept.ID, nil
}
