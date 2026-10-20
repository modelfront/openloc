package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Contract tests validate that API JSON responses match the TypeScript interfaces
// in web/src/types/generated.ts. If a field is renamed or removed in Go, these tests break,
// preventing silent Go/TS type drift.

// TestContractProjectListResponse validates GET /api/projects matches TS Project interface.
func TestContractProjectListResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	testutil.SeedProject(t, q, tenant.ID, "Test", "en", []string{"fr", "de"})

	rec := testutil.DoRequest(t, router, "GET", "/api/projects", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// /api/projects is cursor-paginated (httputil.Paginated): the
	// response is {data: [...], cursor: 0, has_more: false} with cursor as a
	// number, so decode data as RawMessage first.
	var envelope map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &envelope)
	var data []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected at least 1 project")
	}

	// TS: interface Project — see web/src/types/generated.ts. Pins
	// status / customer_id / created_by too, so accidental JSON tag drops
	// are caught by the contract gate.
	requiredKeys := []string{
		"id", "name", "description", "source_language", "target_languages",
		"template_id", "created_at", "updated_at",
		"status", "customer_id", "created_by",
	}
	assertKeysPresent(t, "Project", data[0], requiredKeys)
}

// TestContractTMListResponse validates GET /api/tms matches TS TMCollection interface.
func TestContractTMListResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	testutil.SeedTM(t, q, tenant.ID, "Test TM")

	rec := testutil.DoRequest(t, router, "GET", "/api/tms", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var raw map[string][]map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &raw)

	if len(raw["data"]) == 0 {
		t.Fatal("expected at least 1 TM")
	}

	// TS: interface TMCollection { id, name, created_at, updated_at }
	requiredKeys := []string{"id", "name", "created_at", "updated_at"}
	assertKeysPresent(t, "TMCollection", raw["data"][0], requiredKeys)
}

// TestContractTemplateListResponse validates GET /api/templates matches TS Template interface.
func TestContractTemplateListResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	testutil.SeedTemplate(t, q, tenant.ID, "Test Template")

	rec := testutil.DoRequest(t, router, "GET", "/api/templates", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// The list is cursor-paginated, so the envelope carries `cursor` and
	// `has_more` beside `data` — only `data` is the contract under test here.
	var raw struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &raw)

	if len(raw.Data) == 0 {
		t.Fatal("expected at least 1 template")
	}

	// TS: interface Template { id, name, created_at, updated_at, tenant_id, settings_profile_id }.
	// mt_on_upload + fuzzy_threshold come from settings_profile via
	// GET /api/templates/:id (templateResponse), NOT the list endpoint — list
	// returns the raw db.Template rows for cheapness.
	requiredKeys := []string{"id", "name", "created_at", "updated_at", "tenant_id", "settings_profile_id"}
	assertKeysPresent(t, "Template", raw.Data[0], requiredKeys)
}

// TestContractAnalysisV2Response validates GET /api/projects/:id/analysis/v2
// matches TS ProjectAnalysisV2Response interface.
func TestContractAnalysisV2Response(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Test", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "t.txt", db.FileFormatJsonI18n)
	testutil.SeedJob(t, q, file.ID, "es")

	// Seed one segment + translation so we get file entries
	seg := testutil.SeedSegment(t, q, file.ID, "Test segment.", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	_, _ = pool.Exec(testutil.Ctx(), `UPDATE translations SET origin = 'mt' WHERE id = $1`, tr.ID)

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/analysis/v2", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var raw map[string]map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &raw)

	data := raw["data"]

	// TS: interface ProjectAnalysisV2Response { files, languages, total, total_repetitions }
	assertRawKeysPresent(t, "ProjectAnalysisV2Response", data, []string{"files", "languages", "total", "total_repetitions"})

	// Validate total (AnalysisResponse)
	var total map[string]json.RawMessage
	json.Unmarshal(data["total"], &total)
	assertRawKeysPresent(t, "AnalysisResponse (total)", total, []string{
		"tm_exact", "tm_fuzzy_95", "tm_fuzzy_85", "tm_fuzzy_75", "tm_fuzzy_50",
		"qe", "new", "total", "qp",
	})

	// Validate MatchBand shape
	var band map[string]json.RawMessage
	json.Unmarshal(total["tm_exact"], &band)
	assertRawKeysPresent(t, "MatchBand", band, []string{"segments", "words", "characters", "weighted_words", "weight_pct"})

	// Validate QPBreakdown shape
	var qp map[string]json.RawMessage
	json.Unmarshal(total["qp"], &qp)
	assertRawKeysPresent(t, "QPBreakdown", qp, []string{
		"approved", "ape_approved", "ape_needs_review", "rejected", "not_run", "saved_from_review",
	})

	// Validate FileAnalysis shape
	var files []map[string]json.RawMessage
	json.Unmarshal(data["files"], &files)
	if len(files) == 0 {
		t.Fatal("expected at least 1 file in analysis")
	}
	assertRawKeysPresent(t, "FileAnalysis", files[0], []string{
		"file_id", "file_name", "target_language", "analysis", "repetitions",
	})
}

// TestContractJobAnalysisResponse validates GET /api/jobs/:id/analysis
// matches TS AnalysisResponse interface.
func TestContractJobAnalysisResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Test", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "j.txt", db.FileFormatJsonI18n)
	job := testutil.SeedJob(t, q, file.ID, "es")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d/analysis", job.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var raw map[string]map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &raw)

	// TS: AnalysisResponse
	assertRawKeysPresent(t, "AnalysisResponse (job)", raw["data"], []string{
		"tm_exact", "tm_fuzzy_95", "tm_fuzzy_85", "tm_fuzzy_75", "tm_fuzzy_50",
		"qe", "new", "total", "qp",
	})
}

// TestContractWeightsResponse validates GET /api/analytics/weights matches TS WeightEntry[].
func TestContractWeightsResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")

	rec := testutil.DoRequest(t, router, "GET", "/api/analytics/weights", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var raw struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &raw)

	if len(raw.Data) == 0 {
		t.Fatal("expected at least 1 weight entry (defaults)")
	}

	// TS: interface WeightEntry { band, weight_pct }
	assertKeysPresent(t, "WeightEntry", raw.Data[0], []string{"band", "weight_pct"})
}

// TestContractProductivityResponse validates GET /api/projects/:id/productivity
// returns the ProductivityEntry shape.
func TestContractProductivityResponse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ContractCo")
	user := testutil.SeedUser(t, q, tenant.ID, "contract@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Test", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "p.txt", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Productivity test.", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	// Productivity counts only translations with an editor and editing time.
	if _, err := pool.Exec(testutil.Ctx(),
		`UPDATE translations SET edited_by = $1, editing_time_ms = 5000, edit_distance = 3 WHERE id = $2`,
		user.ID, tr.ID); err != nil {
		t.Fatalf("mark translation edited: %v", err)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/productivity", project.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var raw struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &raw)
	if len(raw.Data) != 1 {
		t.Fatalf("expected 1 productivity entry, got %d", len(raw.Data))
	}

	assertKeysPresent(t, "ProductivityEntry", raw.Data[0], []string{
		"user_id", "user_name", "target_language", "segments_edited", "words_edited",
		"total_editing_time_ms", "words_per_hour", "avg_edit_distance",
	})
}

// --- Helpers ---

func assertKeysPresent(t *testing.T, typeName string, obj map[string]json.RawMessage, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			t.Errorf("%s: missing JSON key %q (Go struct field not serialized or renamed?)", typeName, key)
		}
	}
}

func assertRawKeysPresent(t *testing.T, typeName string, obj map[string]json.RawMessage, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			t.Errorf("%s: missing JSON key %q (Go struct field not serialized or renamed?)", typeName, key)
		}
	}
}
