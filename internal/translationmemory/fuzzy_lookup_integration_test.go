package translationmemory_test

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

// The Matches panel's fuzzy lookup in a large memory is served by the trigram
// index, not by reading every entry of the memory. At 120k entries the planner
// picks the index on its own once the query can use it.
func TestIntegrationFuzzyLookup_UsesTrigramIndex(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "Fuzzy-Index-Test")
	user := testutil.SeedUser(t, q, tenant.ID, "test@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Fuzzy Project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	tm := testutil.SeedTM(t, q, tenant.ID, "Big TM")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 0,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "es", 120000, func(i int) (string, string) {
		return fmt.Sprintf("entry %x", md5.Sum([]byte(strconv.Itoa(i)))), fmt.Sprintf("target %d", i)
	})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Reset your password now", TargetText: "Restablece tu contraseña", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE translation_memory`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	router := testutil.NewTestRouter(pool)
	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Reset your password"))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Restablece tu contraseña") {
		t.Fatalf("the fuzzy match is missing from the panel: %s", rec.Body.String())
	}

	plan := testutil.Explain(t, pool, testutil.GeneratedSQL(t, "FuzzyLookupTM"),
		"Reset your password", tm.ID, "en", "es", translationmemory.FindRecallThreshold, 20)
	if !strings.Contains(plan, "idx_tm_source_trgm") {
		t.Errorf("the fuzzy lookup does not use idx_tm_source_trgm:\n%s", plan)
	}
}

// oldFuzzyLookup is the candidate search without the trigram operator: every
// entry of the memory whose similarity reaches the threshold.
const oldFuzzyLookup = `
SELECT source_text FROM translation_memory
WHERE tm_id = $1
  AND SPLIT_PART(source_language, '-', 1) = SPLIT_PART($2::text, '-', 1)
  AND SPLIT_PART(target_language, '-', 1) = SPLIT_PART($3::text, '-', 1)
  AND similarity(source_text, $4::text) >= $5::real`

// The index-served lookup finds the same candidates as a full similarity
// scan, at pg_trgm's default threshold (0.3, with an entry exactly at it) and
// below it (0.2, with entries between 0.2 and 0.3 that the default would
// drop), and nothing below the threshold.
func TestIntegrationFuzzyLookup_SameCandidatesAsFullScan(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "Fuzzy-Recall-Test")
	tm := testutil.SeedTM(t, q, tenant.ID, "Recall TM")
	const query = "reset password"
	sources := []string{
		"reset password", "reset passwords", "reset pass", "reset", "password",
		"reset kiwi",      // exactly 0.3
		"reset kiwis",     // 0.29, between 0.2 and 0.3
		"reset the modem", // 0.24, between 0.2 and 0.3
		"reset it", "reset now", "resets", "something else entirely",
	}
	for _, src := range sources {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: src, TargetText: "t " + src, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
	}
	var atThreshold bool
	if err := pool.QueryRow(ctx, `SELECT similarity('reset kiwi', $1) = 0.3::real`, query).Scan(&atThreshold); err != nil {
		t.Fatal(err)
	}
	if !atThreshold {
		t.Fatal("fixture: 'reset kiwi' is no longer exactly at 0.3")
	}

	for _, tc := range []struct {
		threshold   float32
		mustHave    []string
		mustNotHave []string
	}{
		{0.3, []string{"reset kiwi"}, []string{"reset kiwis", "reset the modem"}},
		{0.2, []string{"reset kiwi", "reset kiwis", "reset the modem"}, []string{"something else entirely"}},
	} {
		var want []string
		rows, err := pool.Query(ctx, oldFuzzyLookup, tm.ID, "en", "es", query, tc.threshold)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			want = append(want, s)
		}
		rows.Close()

		var got []string
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			hits, err := translationmemory.FuzzyLookup(ctx, db.New(tx), db.FuzzyLookupTMParams{
				TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", Query: query,
				Threshold: tc.threshold, EntryLimit: 100,
			})
			for _, h := range hits {
				got = append(got, h.SourceText)
			}
			return err
		}); err != nil {
			t.Fatalf("FuzzyLookup: %v", err)
		}

		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("threshold %.1f: candidates differ from the full scan:\n got  %q\n want %q", tc.threshold, got, want)
		}
		for _, s := range tc.mustHave {
			if !contains(got, s) {
				t.Errorf("threshold %.1f: %q missing from %q", tc.threshold, s, got)
			}
		}
		for _, s := range tc.mustNotHave {
			if contains(got, s) {
				t.Errorf("threshold %.1f: %q is below it but found", tc.threshold, s)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
