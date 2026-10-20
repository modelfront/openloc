package translationmemory

import (
	"context"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// FuzzyLookup runs FuzzyLookupTM with pg_trgm's similarity threshold set to
// arg.Threshold, so the trigram index finds the candidates. q must be bound to
// a transaction: the threshold lasts until that transaction ends.
func FuzzyLookup(ctx context.Context, q db.Querier, arg db.FuzzyLookupTMParams) ([]db.FuzzyLookupTMRow, error) {
	if err := q.SetTrgmSimilarityThreshold(ctx, arg.Threshold); err != nil {
		return nil, err
	}
	return q.FuzzyLookupTM(ctx, arg)
}

// fuzzyLookup is FuzzyLookup in a transaction of its own.
func (s *Service) fuzzyLookup(ctx context.Context, arg db.FuzzyLookupTMParams) ([]db.FuzzyLookupTMRow, error) {
	var rows []db.FuzzyLookupTMRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		rows, err = FuzzyLookup(ctx, db.New(tx), arg)
		return err
	})
	return rows, err
}
