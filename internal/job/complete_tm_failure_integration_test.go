package job_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// completeTMFixture is a job with one approved translation per source text and
// a write TM, plus a trigger that refuses TM writes as raise says until drop.
type completeTMFixture struct {
	pool  *pgxpool.Pool
	q     db.Querier
	job   db.Job
	tmID  int64
	call  func() int
	drop  func()
	count func() int64
}

func newCompleteTMFixture(t *testing.T, name, raise string, sources ...string) completeTMFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	// t.Cleanup, not defer: the trigger is dropped (registered below) before
	// the pool closes.
	t.Cleanup(cleanup)
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, name)
	user := testutil.SeedUser(t, q, tenant.ID, "u@"+name+".test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, name, "en", []string{"de"})
	tm := testutil.SeedTM(t, q, tenant.ID, "Write TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	for i, src := range sources {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
		text := "DE " + src
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &text}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION test_refuse_tm_write() RETURNS trigger AS $$
		BEGIN `+raise+` RETURN NEW; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER test_refuse_tm_write BEFORE INSERT OR UPDATE ON translation_memory
		FOR EACH ROW EXECUTE FUNCTION test_refuse_tm_write();`); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_refuse_tm_write ON translation_memory;
			DROP FUNCTION IF EXISTS test_refuse_tm_write();`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	}
	t.Cleanup(drop)
	return completeTMFixture{
		pool: pool, q: q, job: job, tmID: tm.ID, drop: drop,
		call: func() int {
			return testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/complete", job.ID), nil, user.ID, tenant.ID).Code
		},
		count: func() int64 {
			return testutil.CountRows(t, pool, `SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tm.ID)
		},
	}
}

// When the approved segments cannot be written to the TM, Complete fails and
// the job stays open, so completing again writes them; it never completes the
// job with the TM write lost.
func TestIntegrationComplete_FailedTMWriteKeepsTheJobOpen(t *testing.T) {
	f := newCompleteTMFixture(t, "complete-tm-fail", `RAISE EXCEPTION 'tm write refused';`, "Hello")
	ctx := context.Background()

	if code := f.call(); code == http.StatusOK {
		t.Errorf("Complete answered 200 with the TM write refused")
	}
	if got, err := f.q.GetJob(ctx, f.job.ID); err != nil || string(got.Status) == "completed" {
		t.Fatalf("job after a failed TM write: status %q, err %v; want it still open", got.Status, err)
	}

	f.drop()
	if code := f.call(); code != http.StatusOK {
		t.Fatalf("Complete after the database recovered: %d", code)
	}
	if got, _ := f.q.GetJob(ctx, f.job.ID); string(got.Status) != "completed" || f.count() != 1 {
		t.Errorf("after retry: status %q, %d TM entries; want completed with 1", got.Status, f.count())
	}
}

// A segment the TM can never store (here refused with Postgres' "index row
// too large" code) is skipped: Complete still completes the job and writes the
// other segments, since retrying could never help.
func TestIntegrationComplete_UnstorableSegmentDoesNotBlockTheJob(t *testing.T) {
	f := newCompleteTMFixture(t, "complete-tm-unstorable",
		`IF NEW.source_text = 'Too long' THEN RAISE EXCEPTION 'index row size exceeds btree maximum' USING ERRCODE = '54000'; END IF;`,
		"Hello", "Too long")
	ctx := context.Background()

	if code := f.call(); code != http.StatusOK {
		t.Fatalf("Complete with one unstorable segment: %d, want 200", code)
	}
	if got, _ := f.q.GetJob(ctx, f.job.ID); string(got.Status) != "completed" || f.count() != 1 {
		t.Errorf("status %q, %d TM entries; want completed with the storable one", got.Status, f.count())
	}
}
