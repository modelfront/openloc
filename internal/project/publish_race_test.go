package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/project"
	"openloc/internal/testutil"
)

// lostPublishRace answers publish as if another publish of the draft flipped
// its status first, and fails the read that follows with readErr.
type lostPublishRace struct {
	db.Querier
	reads   int
	readErr error
}

func (q *lostPublishRace) PublishProjectDraft(ctx context.Context, arg db.PublishProjectDraftParams) (db.Project, error) {
	if _, err := q.Querier.PublishProjectDraft(ctx, arg); err != nil {
		return db.Project{}, err
	}
	return db.Project{}, pgx.ErrNoRows
}

func (q *lostPublishRace) GetProject(ctx context.Context, arg db.GetProjectParams) (db.Project, error) {
	q.reads++
	if q.reads > 1 {
		return db.Project{}, q.readErr
	}
	return q.Querier.GetProject(ctx, arg)
}

// A publish that lost the race reads the project again to return it; when
// that read fails, the failure is the error, not "project not found".
func TestIntegrationPublishLostRaceReadFails(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "PublishRaceCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "race@test.com", "pass123")
	draft := testutil.SeedProject(t, q, tenant.ID, "Race", "en", []string{"fr"})
	if _, err := pool.Exec(ctx, `UPDATE projects SET status = 'draft' WHERE id = $1`, draft.ID); err != nil {
		t.Fatal(err)
	}

	readErr := errors.New("connection reset")
	h := project.NewHandler(&lostPublishRace{Querier: q, readErr: readErr}, pool)
	_, err := h.Publish(ctx, tenant.ID, user.ID, draft.ID, false)
	if errors.Is(err, pgx.ErrNoRows) || !errors.Is(err, readErr) {
		t.Fatalf("Publish = %v, want the failed read's error", err)
	}
}
