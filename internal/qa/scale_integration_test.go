package qa_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// qaScale is a project with a QA profile, one file and its job, served by a
// router whose statements are counted.
type qaScale struct {
	pool      *pgxpool.Pool
	q         db.Querier
	router    http.Handler
	log       *testutil.QueryLog
	userID    int64
	tenantID  int64
	projectID int64
	fileID    int64
	jobID     int64
}

func newQAScale(t *testing.T, checks ...string) qaScale {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	countPool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(countPool)
	userID, tenantID := testutil.RegisterUser(t, q, "qascale")

	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "QA Scale", "source_language": "en", "target_languages": []string{"de"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &created)
	projectID := created.Data.ID

	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{"name": "Scale"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &created)
	for _, check := range checks {
		rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", created.Data.ID),
			map[string]any{"check_type": check, "enabled": true, "severity": "warning"}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": created.Data.ID}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	file := testutil.SeedFile(t, q, projectID, "scale.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	return qaScale{pool: pool, q: q, router: router, log: log, userID: userID, tenantID: tenantID,
		projectID: projectID, fileID: file.ID, jobID: job.ID}
}

// addSegment adds one segment at position pos with a German target.
func (f qaScale) addSegment(t *testing.T, pos int, source, target string) int64 {
	t.Helper()
	seg := testutil.SeedSegment(t, f.q, f.fileID, source, int32(pos))
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", db.TranslationStatusNeedsReview)
	if _, err := f.q.UpdateTranslationHuman(context.Background(), db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &target}); err != nil {
		t.Fatalf("set target: %v", err)
	}
	return tr.ID
}

// addTerms creates a term base with the given en→de concepts and assigns it to
// the project.
func (f qaScale) addTerms(t *testing.T, pairs map[string]string) {
	t.Helper()
	ctx := context.Background()
	tb := testutil.SeedTermBase(t, f.q, f.tenantID, "Scale terms", "en", "de")
	for src, tgt := range pairs {
		var conceptID int64
		if err := f.pool.QueryRow(ctx, `INSERT INTO term_concept (term_base_id) VALUES ($1) RETURNING id`, tb.ID).Scan(&conceptID); err != nil {
			t.Fatalf("concept: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `INSERT INTO term (concept_id, language, text) VALUES ($1, 'en', $2), ($1, 'de', $3)`,
			conceptID, src, tgt); err != nil {
			t.Fatalf("terms: %v", err)
		}
	}
	rec := testutil.DoRequest(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/terminology", f.projectID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": tb.ID, "source_language": "en", "target_language": "de", "mode": "read", "priority": 0},
		},
	}, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func (f qaScale) runJob(t *testing.T) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", f.jobID), nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

func (f qaScale) runOne(t *testing.T, translationID int64) {
	t.Helper()
	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/translations/%d/qa", translationID), nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// findings is every stored finding of the file as "translation|check|message",
// sorted.
func (f qaScale) findings(t *testing.T) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `
		SELECT qr.translation_id, qr.check_type, qr.message FROM qa_results qr
		JOIN translations t ON t.id = qr.translation_id
		JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1`, f.fileID)
	if err != nil {
		t.Fatalf("list findings: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		var check, msg string
		if err := rows.Scan(&id, &check, &msg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%d|%s|%s", id, check, msg))
	}
	sort.Strings(out)
	return out
}

// A job-wide run writes its findings in batches: the statement count must not
// grow with the number of segments that have findings.
func TestIntegrationQAJobRun_WritesInBatches(t *testing.T) {
	f := newQAScale(t, "number_consistency")
	// "Bulk source N" carries a number the target drops: every segment has a finding.
	testutil.SeedSegmentsBulk(t, f.pool, f.fileID, "de", 0, 2000, "Ein Satz ohne Zahl.")

	f.log.Reset()
	f.runJob(t)
	if got := len(f.findings(t)); got != 2000 {
		t.Fatalf("findings = %d, want 2000", got)
	}
	if n := f.log.Total(); n > 100 {
		t.Errorf("a 2000-segment run sent %d statements, want at most 100", n)
	}
}

// Terminology QA in a job run loads the glossary once, not once per segment.
func TestIntegrationQAJobRun_LoadsTermsOnce(t *testing.T) {
	f := newQAScale(t, "terminology")
	terms := make(map[string]string, 200)
	for i := 0; i < 200; i++ {
		terms[fmt.Sprintf("widget%d", i)] = fmt.Sprintf("Bauteil%d", i)
	}
	f.addTerms(t, terms)
	testutil.SeedSegmentsBulk(t, f.pool, f.fileID, "de", 0, 300, "Ein Satz.")

	f.log.Reset()
	f.runJob(t)
	if n := f.log.Total(); n > 100 {
		t.Errorf("a 300-segment terminology run sent %d statements, want at most 100", n)
	}
}

// The consistency check in a job run reads the file's repeated sources once
// per batch, not once per segment.
func TestIntegrationQAJobRun_ConsistencyReadsInBatches(t *testing.T) {
	f := newQAScale(t, "consistency")
	testutil.SeedSegmentsBulk(t, f.pool, f.fileID, "de", 0, 1000, "Ein Satz.")

	f.log.Reset()
	f.runJob(t)
	if n := f.log.Total(); n > 100 {
		t.Errorf("a 1000-segment consistency run sent %d statements, want at most 100", n)
	}
}

// The single-segment consistency lookup (every autosave) finds the file's
// segments with the same source through an index, not by reading the file.
func TestIntegrationQAConsistencyLookup_UsesSourceIndex(t *testing.T) {
	f := newQAScale(t, "consistency")
	testutil.SeedSegmentsBulk(t, f.pool, f.fileID, "de", 0, 5000, "Ein Satz.")
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `ANALYZE segments`); err != nil {
		t.Fatal(err)
	}
	plan := testutil.Explain(t, f.pool, testutil.GeneratedSQL(t, "ListOtherTargetsForSource"),
		f.fileID, "de", "Bulk source 7", int64(0))
	if !strings.Contains(plan, "idx_segments_file_source_md5") {
		t.Errorf("consistency lookup does not use the source index:\n%s", plan)
	}
}

// A job run and a one-segment run (the autosave path) store the same
// findings: the batched job run matches terms and repeated sources exactly
// as the per-segment SQL does, wildcard characters and letter case included.
func TestIntegrationQAJobRun_SameFindingsAsSegmentRuns(t *testing.T) {
	f := newQAScale(t, "terminology", "forbidden_terms", "consistency")
	f.addTerms(t, map[string]string{
		"e-mail":  "E-Mail",
		"Über":    "Über",
		"50%_off": "Rabatt",
		"C++":     "C++",
		"a\\_b":   "AB",
		"server":  "Server",
	})
	// A forbidden target is enforced only for the concepts whose source term the
	// segment's source matches, so these pin the match itself: wildcards,
	// escapes and letter case.
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO term (concept_id, language, text, status)
		SELECT t.concept_id, 'de', v.forbidden, 'forbidden'
		FROM term t JOIN (VALUES ('server', 'Rechner'), ('50%_off', 'Nachlass'), ('a\_b', 'axb'), ('Über', 'alles'))
		     AS v(src, forbidden) ON v.src = t.text`); err != nil {
		t.Fatal(err)
	}
	ids := []int64{
		f.addSegment(t, 1, "Send an E-MAIL to the server.", "Senden Sie eine Nachricht an den Rechner."),
		f.addSegment(t, 2, "über alles", "über alles"),
		f.addSegment(t, 3, "Get 50% off now and 50x offers", "Jetzt Rabatt"),
		f.addSegment(t, 4, "Learn C++ today", "Lerne C heute"),
		f.addSegment(t, 5, "a_b and axb", "AB und axb"),
		f.addSegment(t, 6, "Repeat me", "Wiederhole mich"),
		f.addSegment(t, 7, "Repeat me", "Wiederhol mich"),
		f.addSegment(t, 8, "Repeat me", "Wiederhole mich"),
		f.addSegment(t, 9, "Nothing here", "Nichts hier"),
		f.addSegment(t, 10, "Get 50off", "Nachlass"),      // "50%_off" needs a character before "off": no match
		f.addSegment(t, 11, "Get 50 % off", "Nachlass"),   // matches "50%_off"
		f.addSegment(t, 12, "ab and axb", "axb"),          // "a\_b" is the literal "a_b": no match
		f.addSegment(t, 13, "ÜBER alles", "über alles"),   // case-insensitive match of "Über"
		f.addSegment(t, 14, "the SERVERS", "die Rechner"), // substring match of "server"
	}

	for _, id := range ids {
		f.runOne(t, id)
	}
	perSegment := f.findings(t)
	for _, check := range []string{"|terminology|", "|forbidden_terms|", "|consistency|"} {
		if !strings.Contains(strings.Join(perSegment, "\n"), check) {
			t.Fatalf("precondition: the fixture must produce a %s finding, got:\n%s", check, strings.Join(perSegment, "\n"))
		}
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM qa_results`); err != nil {
		t.Fatal(err)
	}
	f.runJob(t)
	if got := f.findings(t); strings.Join(got, "\n") != strings.Join(perSegment, "\n") {
		t.Errorf("job run findings differ from per-segment runs\njob:\n%s\nper segment:\n%s",
			strings.Join(got, "\n"), strings.Join(perSegment, "\n"))
	}
}
