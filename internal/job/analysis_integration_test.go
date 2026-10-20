package job_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// TestIntegrationAnalysisV2Bands verifies that analysis V2 correctly classifies
// translations into all match bands: 100%, 95-99%, 85-94%, 75-84%, 50-74%, New,
// and QE (QP approved / APE approved).
func TestIntegrationAnalysisV2Bands(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "AnalysisCo")
	user := testutil.SeedUser(t, q, tenant.ID, "analyst@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Analysis Test", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "test.txt", db.FileFormatJsonI18n)

	// Seed the `origin` (translation_origin_v2) + `tm_match_percent`
	// columns directly; Analysis reads only these. "Human-confirmed
	// exact" maps to origin='tm', percent=100 under sticky semantics (the
	// row originally came from a TM match and stays attributed to it).
	type segSpec struct {
		text           string
		origin         string // 'tm' | 'mt' | 'ape' | 'file_import' | "" (NULL)
		tmMatchPercent *int   // 0..101 when origin='tm'
		predictOutcome *string
	}

	p := ptr.To[int]
	s := ptr.To[string]

	specs := []segSpec{
		// TM Exact matches (100%) — pipeline-stamped origin='tm'
		{"Exact match one.", "tm", p(100), nil},
		{"Exact match two.", "tm", p(100), nil},
		// Sticky semantics: human-edited TM-exact still origin='tm'
		{"Human confirmed exact.", "tm", p(100), nil},

		// 95-99% fuzzy
		{"Fuzzy 97 match.", "tm", p(97), nil},

		// 85-94% fuzzy
		{"Fuzzy 90 match.", "tm", p(90), nil},
		{"Fuzzy 88 match.", "tm", p(88), nil},

		// 75-84% fuzzy
		{"Fuzzy 80 match.", "tm", p(80), nil},

		// 50-74% fuzzy
		{"Fuzzy 60 match.", "tm", p(60), nil},
		{"Fuzzy 55 match.", "tm", p(55), nil},

		// MT (no TM match) → "New" band
		{"MT segment one.", "mt", nil, nil},
		{"MT segment two.", "mt", nil, nil},
		{"MT segment three.", "mt", nil, nil},

		// QP approved (MT + approved) → "QE" band
		{"QP approved segment.", "mt", nil, s("approved")},

		// APE approved → "QE" band
		{"APE approved segment.", "mt", nil, s("ape_approved")},

		// APE needs review → still "New" (not auto-approved)
		{"APE needs review.", "mt", nil, s("ape_needs_review")},

		// rejected → still "New"
		{"QP rejected.", "mt", nil, s("rejected")},
	}

	for i, spec := range specs {
		seg := testutil.SeedSegment(t, q, file.ID, spec.text, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

		// Set origin / tm_match_percent / predict_outcome via raw SQL.
		query := `UPDATE translations SET origin = $2::translation_origin`
		args := []any{tr.ID, spec.origin}

		if spec.tmMatchPercent != nil {
			query += `, tm_match_percent = $3`
			args = append(args, *spec.tmMatchPercent)
		}
		if spec.predictOutcome != nil {
			idx := len(args) + 1
			query += fmt.Sprintf(`, predict_outcome = $%d`, idx)
			args = append(args, *spec.predictOutcome)
		}
		query += ` WHERE id = $1`

		_, err := pool.Exec(testutil.Ctx(), query, args...)
		if err != nil {
			t.Fatalf("update translation %d: %v", i, err)
		}
	}

	// Also seed a job so the endpoint works
	testutil.SeedJob(t, q, file.ID, "es")

	// Fetch analysis V2
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Total struct {
				TMExact   bandJSON `json:"tm_exact"`
				TMFuzzy95 bandJSON `json:"tm_fuzzy_95"`
				TMFuzzy85 bandJSON `json:"tm_fuzzy_85"`
				TMFuzzy75 bandJSON `json:"tm_fuzzy_75"`
				TMFuzzy50 bandJSON `json:"tm_fuzzy_50"`
				QE        bandJSON `json:"qe"`
				New       bandJSON `json:"new"`
				Total     bandJSON `json:"total"`
			} `json:"total"`
			Files []struct {
				FileID     int64  `json:"file_id"`
				FileName   string `json:"file_name"`
				TargetLang string `json:"target_language"`
			} `json:"files"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	total := resp.Data.Total

	// Verify band segment counts
	tests := []struct {
		name     string
		band     bandJSON
		wantSegs int32
	}{
		{"TMExact (100%)", total.TMExact, 3},       // 2 tm_exact + 1 human w/ score=1.0
		{"TMFuzzy95 (95-99%)", total.TMFuzzy95, 1}, // 1 at 0.97
		{"TMFuzzy85 (85-94%)", total.TMFuzzy85, 2}, // 2 at 0.90, 0.88
		{"TMFuzzy75 (75-84%)", total.TMFuzzy75, 1}, // 1 at 0.80
		{"TMFuzzy50 (50-74%)", total.TMFuzzy50, 2}, // 2 at 0.60, 0.55
		{"QE", total.QE, 2},                        // 1 approved + 1 ape_approved
		{"New", total.New, 5},                      // 3 plain MT + 1 ape_needs_review + 1 rejected
		{"Total", total.Total, 16},                 // all segments
	}

	for _, tc := range tests {
		if tc.band.Segments != tc.wantSegs {
			t.Errorf("%s: want %d segments, got %d", tc.name, tc.wantSegs, tc.band.Segments)
		}
	}

	// Verify file-level grouping
	if len(resp.Data.Files) != 1 {
		t.Fatalf("expected 1 file entry, got %d", len(resp.Data.Files))
	}
	if resp.Data.Files[0].FileName != "test.txt" {
		t.Errorf("expected file name 'test.txt', got %q", resp.Data.Files[0].FileName)
	}
	if resp.Data.Files[0].TargetLang != "es" {
		t.Errorf("expected target language 'es', got %q", resp.Data.Files[0].TargetLang)
	}
}

// TestIntegrationAnalysisV2Repetitions verifies that repeated source segments
// are detected and counted correctly.
func TestIntegrationAnalysisV2Repetitions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RepsCo")
	user := testutil.SeedUser(t, q, tenant.ID, "reps@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Reps Test", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "reps.txt", db.FileFormatJsonI18n)

	// Create 3 segments with same source text (= 2 repetitions) + 1 unique
	sources := []string{
		"Repeated segment.",
		"Repeated segment.",
		"Repeated segment.",
		"Unique segment.",
	}
	for i, src := range sources {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusNeedsReview)
		_, err := pool.Exec(testutil.Ctx(), `UPDATE translations SET origin = 'mt'::translation_origin WHERE id = $1`, tr.ID)
		if err != nil {
			t.Fatalf("update translation: %v", err)
		}
	}

	testutil.SeedJob(t, q, file.ID, "fr")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			TotalRepetitions bandJSON `json:"total_repetitions"`
			Files            []struct {
				Repetitions bandJSON `json:"repetitions"`
			} `json:"files"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	// 3 segments with same text → 2 are repetitions (first occurrence excluded)
	if resp.Data.TotalRepetitions.Segments != 2 {
		t.Errorf("expected 2 total repetitions, got %d", resp.Data.TotalRepetitions.Segments)
	}
	if len(resp.Data.Files) != 1 {
		t.Fatalf("expected 1 file in the analysis, got %d", len(resp.Data.Files))
	}
	if resp.Data.Files[0].Repetitions.Segments != 2 {
		t.Errorf("expected 2 file repetitions, got %d", resp.Data.Files[0].Repetitions.Segments)
	}
}

// TestIntegrationAnalysisV2ResponseShape validates the full JSON structure of the
// analysis V2 response — all expected keys present with correct types.
// This serves as a snapshot test: if the response shape changes, this test breaks.
func TestIntegrationAnalysisV2ResponseShape(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ShapeCo")
	user := testutil.SeedUser(t, q, tenant.ID, "shape@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Shape Test", "en", []string{"de"})

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Decode into raw map to validate all keys exist
	var raw map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &raw)

	if _, ok := raw["data"]; !ok {
		t.Fatal("missing 'data' key in response")
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw["data"], &data); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}

	requiredKeys := []string{"files", "languages", "total", "total_repetitions"}
	for _, key := range requiredKeys {
		if _, ok := data[key]; !ok {
			t.Errorf("missing key 'data.%s' in response", key)
		}
	}

	// Validate total has all analysis band keys
	var total map[string]json.RawMessage
	if err := json.Unmarshal(data["total"], &total); err != nil {
		t.Fatalf("failed to unmarshal total: %v", err)
	}

	analysisKeys := []string{
		"tm_exact", "tm_fuzzy_95", "tm_fuzzy_85", "tm_fuzzy_75", "tm_fuzzy_50",
		"qe", "new", "total", "qp",
	}
	for _, key := range analysisKeys {
		if _, ok := total[key]; !ok {
			t.Errorf("missing key 'data.total.%s'", key)
		}
	}

	// Validate QP breakdown has all outcome keys
	var qp map[string]json.RawMessage
	if err := json.Unmarshal(total["qp"], &qp); err != nil {
		t.Fatalf("failed to unmarshal qp: %v", err)
	}

	qpKeys := []string{"approved", "ape_approved", "ape_needs_review", "rejected", "not_run", "saved_from_review"}
	for _, key := range qpKeys {
		if _, ok := qp[key]; !ok {
			t.Errorf("missing key 'data.total.qp.%s'", key)
		}
	}

	// Validate each band has segments/words/characters
	var band map[string]json.RawMessage
	if err := json.Unmarshal(total["tm_exact"], &band); err != nil {
		t.Fatalf("failed to unmarshal tm_exact band: %v", err)
	}
	bandKeys := []string{"segments", "words", "characters"}
	for _, key := range bandKeys {
		if _, ok := band[key]; !ok {
			t.Errorf("missing key in MatchBand: %s", key)
		}
	}
}

// TestIntegrationJobAnalysisBands verifies the per-job analysis endpoint.
func TestIntegrationJobAnalysisBands(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "JobCo")
	user := testutil.SeedUser(t, q, tenant.ID, "job@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Job Test", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "job.txt", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Seed origin (new enum) + tm_match_percent directly.
	for i, spec := range []struct {
		text    string
		origin  string
		percent *int
	}{
		{"Exact one.", "tm", ptr.To(100)},
		{"MT one.", "mt", nil},
		{"MT two.", "mt", nil},
	} {
		seg := testutil.SeedSegment(t, q, file.ID, spec.text, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

		query := `UPDATE translations SET origin = $2::translation_origin`
		args := []any{tr.ID, spec.origin}
		if spec.percent != nil {
			query += `, tm_match_percent = $3`
			args = append(args, *spec.percent)
		}
		query += ` WHERE id = $1`

		_, err := pool.Exec(testutil.Ctx(), query, args...)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d/analysis", job.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			TMExact bandJSON `json:"tm_exact"`
			New     bandJSON `json:"new"`
			Total   bandJSON `json:"total"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	if resp.Data.TMExact.Segments != 1 {
		t.Errorf("job analysis TMExact: want 1, got %d", resp.Data.TMExact.Segments)
	}
	if resp.Data.New.Segments != 2 {
		t.Errorf("job analysis New: want 2, got %d", resp.Data.New.Segments)
	}
	if resp.Data.Total.Segments != 3 {
		t.Errorf("job analysis Total: want 3, got %d", resp.Data.Total.Segments)
	}
}

// bandJSON matches the MatchBand JSON response shape.
type bandJSON struct {
	Segments   int32 `json:"segments"`
	Words      int32 `json:"words"`
	Characters int32 `json:"characters"`
}
