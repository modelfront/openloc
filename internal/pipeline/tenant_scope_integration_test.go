package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// Each service entry point takes the caller's tenant id. Called with another
// tenant's id and tenant A's file, job or translation, it returns a not-found
// error and writes nothing of tenant A's. Every test then repeats the call with
// tenant A's own id and requires a write, so the fixture is one the
// cross-tenant call would have changed.

// fileTranslationState renders the file's translations and their history row
// counts as one string; any write to them changes it.
func fileTranslationState(t *testing.T, pool *pgxpool.Pool, fileID int64) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT t.id, t.status::text, COALESCE(t.active_text, ''), COALESCE(t.origin::text, ''),
		       COALESCE(t.predict_outcome::text, ''), t.updated_at::text,
		       (SELECT count(*) FROM translation_history th WHERE th.translation_id = t.id)
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 ORDER BY t.id`, fileID)
	if err != nil {
		t.Fatalf("query translations: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, history int64
		var status, text, origin, outcome, updatedAt string
		if err := rows.Scan(&id, &status, &text, &origin, &outcome, &updatedAt, &history); err != nil {
			t.Fatalf("scan translation: %v", err)
		}
		fmt.Fprintf(&b, "%d status=%s text=%q origin=%s outcome=%s updated=%s history=%d\n",
			id, status, text, origin, outcome, updatedAt, history)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read translations: %v", err)
	}
	return b.String()
}

func requireNotFound(t *testing.T, call string, err error) {
	t.Helper()
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("%s with another tenant's id: want a not-found (ErrNoRows) error, got %v", call, err)
	}
}

func TestIntegrationTranslateFile_OtherTenantRejected(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// "Hello world" fills from the TM, "Goodbye" from the mock MT engine.
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world", "Goodbye"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello world", TargetText: "Bonjour le monde", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	other := testutil.SeedTenant(t, q, "OtherCorp")
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	before := fileTranslationState(t, pool, fileID)
	_, err := svc.TranslateFile(ctx, other.ID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	requireNotFound(t, "TranslateFile", err)
	if got := fileTranslationState(t, pool, fileID); got != before {
		t.Fatalf("cross-tenant TranslateFile changed tenant A's translations:\nbefore:\n%safter:\n%s", before, got)
	}

	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("same-tenant TranslateFile: %v", err)
	}
	if got := fileTranslationState(t, pool, fileID); got == before {
		t.Fatalf("same-tenant TranslateFile wrote nothing; the fixture cannot show a cross-tenant write:\n%s", got)
	}
}

func TestIntegrationRunAIStep_OtherTenantRejected(t *testing.T) {
	var sent atomic.Int32
	srv := usageStub(t, func(string) string {
		sent.Add(1)
		return `"approved":true,"score":95`
	})
	e := newAIUsageEnv(t, srv.URL)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`UPDATE translations SET active_text = 'Bonjour le monde', origin = 'mt' WHERE id = $1`, e.tr.ID); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	engine := e.engine(t, "GPT reviewer", "qe_ape", true)
	job := testutil.SeedJob(t, e.q, e.file.ID, "fr")
	routerID := testutil.SeedRouterForEngine(t, e.q, e.tenant.ID, engine.ID)
	testutil.SeedAIJobStepWithRouter(t, e.q, e.project.SettingsProfileID, job.ID, routerID)
	other := testutil.SeedTenant(t, e.q, "OtherCorp")

	before := fileTranslationState(t, e.pool, e.file.ID)
	_, err := e.svc.RunAIStep(ctx, other.ID, job.ID, nil)
	requireNotFound(t, "RunAIStep", err)
	if n := sent.Load(); n != 0 {
		t.Fatalf("cross-tenant RunAIStep sent %d of tenant A's segments to the provider, want 0", n)
	}
	if got := fileTranslationState(t, e.pool, e.file.ID); got != before {
		t.Fatalf("cross-tenant RunAIStep changed tenant A's translations:\nbefore:\n%safter:\n%s", before, got)
	}

	if _, err := e.svc.RunAIStep(ctx, e.tenant.ID, job.ID, nil); err != nil {
		t.Fatalf("same-tenant RunAIStep: %v", err)
	}
	if n := sent.Load(); n != 1 {
		t.Fatalf("same-tenant RunAIStep sent %d segments, want 1", n)
	}
	if got := fileTranslationState(t, e.pool, e.file.ID); got == before {
		t.Fatalf("same-tenant RunAIStep wrote nothing; the fixture cannot show a cross-tenant write:\n%s", got)
	}
}

func TestIntegrationSaveToTM_OtherTenantRejected(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: err=%v len=%d", err, len(rows))
	}
	confirmed := "Bonjour le monde"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: rows[0].ID, HumanText: &confirmed}); err != nil {
		t.Fatalf("approve translation: %v", err)
	}
	other := testutil.SeedTenant(t, q, "OtherCorp")
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	_, err = svc.SaveToTM(ctx, other.ID, rows[0].ID)
	requireNotFound(t, "SaveToTM", err)
	if n := countTMEntriesByID(t, q, tmID, "en", "fr", "Hello world"); n != 0 {
		t.Fatalf("cross-tenant SaveToTM wrote %d entries to tenant A's TM, want 0", n)
	}

	saved, err := svc.SaveToTM(ctx, tenantID, rows[0].ID)
	if err != nil {
		t.Fatalf("same-tenant SaveToTM: %v", err)
	}
	if len(saved) != 1 || saved[0] != tmID {
		t.Fatalf("same-tenant SaveToTM saved to %v, want [%d]", saved, tmID)
	}
	if n := countTMEntriesByID(t, q, tmID, "en", "fr", "Hello world"); n != 1 {
		t.Fatalf("same-tenant SaveToTM left %d entries in the TM, want 1", n)
	}
}

func TestIntegrationSaveApprovedToTM_OtherTenantRejected(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: err=%v len=%d", err, len(rows))
	}
	confirmed := "Bonjour le monde"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: rows[0].ID, HumanText: &confirmed}); err != nil {
		t.Fatalf("approve translation: %v", err)
	}
	other := testutil.SeedTenant(t, q, "OtherCorp")
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	_, err = svc.SaveApprovedToTM(ctx, other.ID, fileID, "en", "fr")
	requireNotFound(t, "SaveApprovedToTM", err)
	if n := countTMEntriesByID(t, q, tmID, "en", "fr", "Hello world"); n != 0 {
		t.Fatalf("cross-tenant SaveApprovedToTM wrote %d entries to tenant A's TM, want 0", n)
	}

	res, err := svc.SaveApprovedToTM(ctx, tenantID, fileID, "en", "fr")
	if err != nil {
		t.Fatalf("same-tenant SaveApprovedToTM: %v", err)
	}
	if res.TMSavedCount != 1 {
		t.Fatalf("same-tenant SaveApprovedToTM saved %d, want 1", res.TMSavedCount)
	}
	if n := countTMEntriesByID(t, q, tmID, "en", "fr", "Hello world"); n != 1 {
		t.Fatalf("same-tenant SaveApprovedToTM left %d entries in the TM, want 1", n)
	}
}
