package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// approvedFileTenants numbers the tenants approvedFile seeds.
var approvedFileTenants int

// approvedFile is a file of n segments "Source <i>" approved in fr as
// "Cible <i>", plus one auto-approved segment, with two write TMs.
func approvedFile(t *testing.T, pool *pgxpool.Pool, q db.Querier, n int) (tenantID, fileID int64, tms [2]db.TmCollection) {
	t.Helper()
	ctx := context.Background()
	approvedFileTenants++
	tenant := testutil.SeedTenant(t, q, fmt.Sprintf("TMWriteScaleCorp %d", approvedFileTenants))
	project := testutil.SeedProject(t, q, tenant.ID, "Write Project", "en", []string{"fr"})
	for i := range tms {
		tms[i] = testutil.SeedTM(t, q, tenant.ID, fmt.Sprintf("Write TM %d", i))
		if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: project.SettingsProfileID, TmID: tms[i].ID, Mode: "read_write", Priority: int32(i),
		}); err != nil {
			t.Fatalf("assign TM: %v", err)
		}
	}
	file := testutil.SeedFile(t, q, project.ID, "write.json", db.FileFormatJsonI18n)
	testutil.Exec(t, pool, `
		WITH s AS (
			INSERT INTO segments (file_id, position, source_text, segment_key)
			SELECT $1, i, 'Source ' || i, 'k' || i FROM generate_series(1, $2::int + 1) i
			RETURNING id, position)
		INSERT INTO translations (segment_id, target_language, status, active_text, confirmed_via)
		SELECT id, 'fr',
		       CASE WHEN position <= $2::int THEN 'approved' ELSE 'auto_approved' END::translation_status,
		       'Cible ' || position, 'user'
		FROM s`, file.ID, n)
	return tenant.ID, file.ID, tms
}

// Writing a file's approved segments to two write TMs looks the project, the
// write TMs, the user and the neighbours up once, and uses one transaction per
// chunk and TM instead of one per segment and TM. The auto-approved segment
// never reaches the TM, and every entry carries its document context.
func TestIntegrationSaveApprovedToTM_BoundedStatements(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	const n = 300
	tenantID, fileID, tms := approvedFile(t, pool, q, n)

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	log.Reset()
	res, err := svc.SaveApprovedToTM(ctx, tenantID, fileID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if res.ApprovedCount != n || res.TMSavedCount != 2*n {
		t.Errorf("result = %+v, want %d approved and %d saved", res, n, 2*n)
	}

	for _, name := range []string{"GetProject", "LookupSettingsWriteTMs", "GetUser"} {
		if c := log.Count(name); c > 1 {
			t.Errorf("%s ran %d times, want at most once", name, c)
		}
	}
	if c := log.Count("GetSegmentNeighborSources"); c > 0 {
		t.Errorf("GetSegmentNeighborSources ran %d times, want the file's neighbours read once", c)
	}
	if c := log.CountPrefix("BEGIN"); c > 8 {
		t.Errorf("%d transactions for %d segments × 2 TMs, want one per chunk and TM", c, n)
	}
	// Two 200-row chunks × 2 TMs, a few statements each, plus the reads.
	if c := log.Total(); c > 60 {
		t.Errorf("%d statements for %d segments × 2 TMs, want a few per chunk, not per segment", c, n)
	}

	for _, tm := range tms {
		var entries, withContext int
		var leaked bool
		if err := pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE source_text = 'Source 2' AND prev_source_hash = $2 AND next_source_hash = $3),
			       bool_or(source_text = $4)
			FROM translation_memory WHERE tm_id = $1`,
			tm.ID, md5hex("Source 1"), md5hex("Source 3"), fmt.Sprintf("Source %d", n+1)).Scan(&entries, &withContext, &leaked); err != nil {
			t.Fatal(err)
		}
		if entries != n {
			t.Errorf("TM %d holds %d entries, want %d", tm.ID, entries, n)
		}
		if withContext != 1 {
			t.Errorf("TM %d: 'Source 2' does not carry its neighbours' hashes", tm.ID)
		}
		if leaked {
			t.Errorf("TM %d received the auto-approved segment", tm.ID)
		}
	}
}

// A segment the TM cannot store (its source is too long for the exact-match
// index) is skipped; the rest of its chunk is still saved.
func TestIntegrationSaveApprovedToTM_UnstorableSegmentSkipsOnlyItself(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	const n = 20
	tenantID, fileID, tms := approvedFile(t, pool, q, n)
	// Random text does not compress, so the index row stays too large.
	var long strings.Builder
	for i := 0; long.Len() < 12000; i++ {
		long.WriteString(md5hex(fmt.Sprint(i)))
	}
	if _, err := pool.Exec(ctx, `UPDATE segments SET source_text = $2 WHERE file_id = $1 AND position = 5`, fileID, long.String()); err != nil {
		t.Fatal(err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	res, err := svc.SaveApprovedToTM(ctx, tenantID, fileID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if res.TMSavedCount != 2*(n-1) {
		t.Errorf("TMSavedCount = %d, want %d (every segment but the long one, in both TMs)", res.TMSavedCount, 2*(n-1))
	}
	for _, tm := range tms {
		var entries int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tm.ID).Scan(&entries); err != nil {
			t.Fatal(err)
		}
		if entries != n-1 {
			t.Errorf("TM %d holds %d entries, want %d", tm.ID, entries, n-1)
		}
	}
}

// SaveTranslationsToTM writes only the listed translations a person approved:
// an auto-approved one, an assistant-confirmed one and another tenant's are
// left out. Two files go in one call with a bounded number of transactions,
// each entry carries its context, an overwritten target is archived under
// the given reason, and updated_by names the user in ctx.
func TestIntegrationSaveTranslationsToTM_OnlyHumanApprovedBounded(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	const n = 250
	tenantID, fileA, tms := approvedFile(t, pool, q, n)

	// A second file in the same project, and another tenant's file.
	var projectID int64
	if err := pool.QueryRow(ctx, `SELECT project_id FROM files WHERE id = $1`, fileA).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	fileB := testutil.SeedFile(t, q, projectID, "second.json", db.FileFormatJsonI18n)
	testutil.Exec(t, pool, `
		WITH s AS (INSERT INTO segments (file_id, position, source_text, segment_key)
		           SELECT $1, i, 'Other ' || i, 'o' || i FROM generate_series(1, 50) i RETURNING id, position)
		INSERT INTO translations (segment_id, target_language, status, active_text, confirmed_via)
		SELECT id, 'fr', 'approved', 'Autre ' || position, 'user' FROM s`, fileB.ID)
	// "Other 1" again at the end of file B: the later target wins.
	testutil.Exec(t, pool, `
		WITH s AS (INSERT INTO segments (file_id, position, source_text, segment_key)
		           VALUES ($1, 51, 'Other 1', 'o51') RETURNING id)
		INSERT INTO translations (segment_id, target_language, status, active_text, confirmed_via)
		SELECT id, 'fr', 'approved', 'Autre dernier', 'user' FROM s`, fileB.ID)
	_, otherFile, _ := approvedFile(t, pool, q, 1)
	testutil.Exec(t, pool, `UPDATE translations t SET confirmed_via = 'assistant' FROM segments s
		WHERE s.id = t.segment_id AND s.file_id = $1 AND s.position = 7`, fileA)
	// An existing entry for "Source 3" with another target is overwritten.
	if _, err := pool.Exec(ctx, `
		INSERT INTO translation_memory (tm_id, source_language, target_language, source_text, target_text, source_hash, target_hash, created_by, updated_by, tu_id)
		VALUES ($1, 'en', 'fr', 'Source 3', 'Ancienne', md5('Source 3'), md5('Ancienne'), 'human', 'human', '')`, tms[0].ID); err != nil {
		t.Fatal(err)
	}

	ids := testutil.QueryInt64s(t, pool, `
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = ANY($1)`, []int64{fileA, fileB.ID, otherFile})

	userID, _ := testutil.RegisterUser(t, q, "tmwriter")
	actx := auth.WithClaims(ctx, &auth.Claims{UserID: userID, TenantID: tenantID})
	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	log.Reset()
	saved, err := svc.SaveTranslationsToTM(actx, tenantID, ids, pipeline.TMReasonCATConfirm)
	if err != nil {
		t.Fatalf("SaveTranslationsToTM: %v", err)
	}
	// File A: n approved minus the assistant one; file B: 51; two TMs each.
	if want := 2 * (n - 1 + 51); saved != want {
		t.Errorf("saved %d writes, want %d", saved, want)
	}
	if c := log.CountPrefix("BEGIN"); c > 8 {
		t.Errorf("%d transactions, want one per chunk, file and TM", c)
	}
	if c := log.Total(); c > 60 {
		t.Errorf("%d statements for %d rows × 2 TMs, want a few per chunk, not per row", c, n-1+51)
	}
	var lastWins string
	if err := pool.QueryRow(ctx, `SELECT target_text FROM translation_memory WHERE tm_id = $1 AND source_text = 'Other 1'`, tms[0].ID).Scan(&lastWins); err != nil {
		t.Fatal(err)
	}
	if lastWins != "Autre dernier" {
		t.Errorf("'Other 1' holds %q, want the later target 'Autre dernier'", lastWins)
	}
	if c := log.Count("GetSegmentNeighborSources"); c > 0 {
		t.Errorf("GetSegmentNeighborSources ran %d times, want neighbours read per file", c)
	}

	var leaked, withContext, archived, byUser int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE source_text IN ('Source 7', $2)),
		       count(*) FILTER (WHERE source_text = 'Other 2' AND prev_source_hash = $3 AND next_source_hash = $4),
		       count(*) FILTER (WHERE updated_by LIKE '%tmwriter%')
		FROM translation_memory WHERE tm_id = $1`,
		tms[0].ID, fmt.Sprintf("Source %d", n+1), md5hex("Other 1"), md5hex("Other 3")).Scan(&leaked, &withContext, &byUser); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM translation_memory_history h JOIN translation_memory m ON m.id = h.entry_id
		WHERE m.tm_id = $1 AND h.target_text = 'Ancienne' AND h.change_reason = $2 AND h.changed_by_user_id = $3`,
		tms[0].ID, pipeline.TMReasonCATConfirm, userID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Errorf("%d assistant-confirmed or auto-approved entries reached the TM", leaked)
	}
	if withContext != 1 {
		t.Error("'Other 2' does not carry its neighbours' hashes")
	}
	if archived != 1 {
		t.Errorf("%d history rows for the overwritten target, want 1 with the reason and user", archived)
	}
	if byUser != n-1+50 {
		t.Errorf("%d entries name the user in updated_by, want %d", byUser, n-1+50)
	}
	var otherTM int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM translation_memory tm JOIN tm_collections c ON c.id = tm.tm_id WHERE c.tenant_id <> $1`, tenantID).Scan(&otherTM); err != nil {
		t.Fatal(err)
	}
	if otherTM != 0 {
		t.Errorf("another tenant's translation reached a TM: %d entries", otherTM)
	}
}

// A call for a few translations of a large file reads the neighbours of those
// segments only, not every segment of the file.
func TestIntegrationSaveTranslationsToTM_ReadsOnlyItsSegments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	const n = 2000
	tenantID, fileID, tms := approvedFile(t, pool, q, n)

	ids := testutil.QueryInt64s(t, pool, `
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND s.position BETWEEN 100 AND 109`, fileID)

	cpool, log := testutil.CountingPool(t)
	svc := pipeline.NewService(db.New(cpool), pipeline.NewPgxTxRunner(cpool), nil)
	log.Reset()
	if _, err := svc.SaveTranslationsToTM(ctx, tenantID, ids, pipeline.TMReasonCATConfirm); err != nil {
		t.Fatalf("SaveTranslationsToTM: %v", err)
	}
	if r := log.TotalRows(); r > 200 {
		t.Errorf("read or wrote %d rows for %d translations of a %d-segment file, want rows for those segments only", r, len(ids), n)
	}
	var ok int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM translation_memory WHERE tm_id = $1 AND source_text = 'Source 105'
		AND prev_source_hash = $2 AND next_source_hash = $3`, tms[0].ID, md5hex("Source 104"), md5hex("Source 106")).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if ok != 1 {
		t.Error("'Source 105' does not carry its neighbours' hashes")
	}
}
