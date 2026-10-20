package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// singleConnPool is a pool of one connection on the test database: work that
// holds a connection while it waits for a second one never finishes on it.
func singleConnPool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// The TM phase's fuzzy lookups never hold a connection while it writes a
// match: on a pool of one connection the phase still fills the fuzzy match.
func TestIntegrationTranslateFile_FuzzyPhaseNeedsOneConnection(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Reset your password now", "Something unrelated entirely"}, 0.5)
	if _, err := q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Reset your password", TargetText: "Réinitialisez votre mot de passe", CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}

	one := singleConnPool(t, pool)
	svc := pipeline.NewService(db.New(one), pipeline.NewPgxTxRunner(one), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	rows, err := q.ListTranslationsByFile(context.Background(), db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	filled := 0
	for _, r := range rows {
		if r.ActiveText != nil {
			filled++
		}
	}
	if filled != 1 {
		t.Errorf("%d segments filled on a one-connection pool, want the fuzzy match written", filled)
	}
}
