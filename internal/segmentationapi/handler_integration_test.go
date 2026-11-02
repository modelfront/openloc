package segmentationapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
	"openloc/internal/testutil"
)

type profileResp struct {
	Data struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		UpdatedAt  string `json:"updated_at"`
		Definition struct {
			Rules []map[string]any `json:"rules"`
		} `json:"definition"`
	} `json:"data"`
}

var httpDef = map[string]any{"rules": []map[string]any{{"before": `[.!]`, "after": `\s`, "break": true}}}

func TestIntegrationSegmentationHTTP_OtherTenantGets404Everywhere(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	a := testutil.SeedTenant(t, q, "SegHTTPA")
	b := testutil.SeedTenant(t, q, "SegHTTPB")
	ownerA := testutil.SeedUser(t, q, a.ID, "a@seg-http.test", "pass")
	ownerB := testutil.SeedUser(t, q, b.ID, "b@seg-http.test", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles",
		map[string]any{"name": "A's", "source_languages": []string{"en"}, "definition": httpDef}, ownerA.ID, a.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created profileResp
	testutil.DecodeJSON(t, rec, &created)
	path := fmt.Sprintf("/api/segmentation-profiles/%d", created.Data.ID)

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", path, nil},
		{"GET", path + "/srx", nil},
		{"PUT", path, map[string]any{"name": "x", "source_languages": []string{"en"}, "definition": httpDef, "updated_at": created.Data.UpdatedAt}},
		{"DELETE", path, nil},
		{"POST", "/api/segmentation-profiles/preview", map[string]any{"id": created.Data.ID, "text": "One. Two."}},
	} {
		rec := testutil.DoRequest(t, router, c.method, c.path, c.body, ownerB.ID, b.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s as another org: %d %s, want 404", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	rec = testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles",
		map[string]any{"name": "Copy", "source_languages": []string{"en"}, "from": created.Data.ID}, ownerB.ID, b.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequest(t, router, "GET", "/api/segmentation-profiles", nil, ownerB.ID, b.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "A's") {
		t.Error("another org's profile is listed")
	}
	// Still there and unchanged for its own org.
	rec = testutil.DoRequest(t, router, "GET", path, nil, ownerA.ID, a.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var after profileResp
	testutil.DecodeJSON(t, rec, &after)
	if after.Data.Name != "A's" {
		t.Errorf("A's profile after B's attempts = %+v", after.Data)
	}
}

func TestIntegrationSegmentationHTTP_WritesAndErrors(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SegHTTPWrites")
	owner := testutil.SeedUser(t, q, tenant.ID, "o@seg-writes.test", "pass")
	do := func(method, path string, body any) (int, string) {
		rec := testutil.DoRequest(t, router, method, path, body, owner.ID, tenant.ID)
		return rec.Code, rec.Body.String()
	}

	// Built-ins: readable, read-only.
	en := builtinID(t, segmentationapi.NewService(q, pool), tenant.ID, "builtin:en@1")
	enPath := fmt.Sprintf("/api/segmentation-profiles/%d", en)
	if code, body := do("GET", enPath, nil); code != http.StatusOK || !strings.Contains(body, `"builtin":true`) || !strings.Contains(body, `"builtin_key":"builtin:en@1"`) {
		t.Errorf("GET %s = %d %s", enPath, code, body)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", enPath},
		{"DELETE", enPath},
	} {
		code, body := do(c.method, c.path, map[string]any{"name": "x", "source_languages": []string{"en"}, "definition": httpDef, "updated_at": "2026-01-01T00:00:00Z"})
		if code != http.StatusConflict || !strings.Contains(body, "read-only") && !strings.Contains(body, "Copy it first") {
			t.Errorf("%s %s = %d %s, want 409", c.method, c.path, code, body)
		}
	}
	if code, _ := do("GET", "/api/segmentation-profiles/999999999", nil); code != http.StatusNotFound {
		t.Errorf("unknown profile = %d, want 404", code)
	}
	if code, _ := do("GET", "/api/segmentation-profiles/builtin:en@1", nil); code != http.StatusBadRequest {
		t.Errorf("a non-numeric id = %d, want 400", code)
	}

	// Validation errors name the problem.
	bad := map[string]any{"rules": []map[string]any{{"before": `(?=x)`, "after": "", "break": true}}}
	code, body := do("POST", "/api/segmentation-profiles", map[string]any{"name": "Bad", "source_languages": []string{"en"}, "definition": bad})
	if code != http.StatusBadRequest || !strings.Contains(body, "Rule 1: the before pattern is not valid: look-ahead is not supported.") {
		t.Errorf("bad pattern = %d %s", code, body)
	}

	// Copy, edit, stale edit.
	code, body = do("POST", "/api/segmentation-profiles", map[string]any{"name": "Legal", "source_languages": []string{"en"}, "from": en})
	if code != http.StatusCreated {
		t.Fatalf("copy = %d %s", code, body)
	}
	rec := testutil.DoRequest(t, router, "GET", "/api/segmentation-profiles?source_language=en-GB", nil, owner.ID, tenant.ID)
	if !strings.Contains(rec.Body.String(), `"Legal"`) || strings.Contains(rec.Body.String(), `"German"`) {
		t.Errorf("list filtered by en-GB = %s, want en profiles and none for German", rec.Body.String())
	}
	var created profileResp
	rec = testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles", map[string]any{"name": "Edit me", "source_languages": []string{"en"}, "definition": httpDef}, owner.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &created)
	path := fmt.Sprintf("/api/segmentation-profiles/%d", created.Data.ID)
	edit := map[string]any{"name": "Edited", "source_languages": []string{"en"}, "definition": httpDef, "updated_at": created.Data.UpdatedAt}
	if code, body := do("PUT", path, edit); code != http.StatusOK {
		t.Fatalf("edit = %d %s", code, body)
	}
	if code, body := do("PUT", path, edit); code != http.StatusConflict {
		t.Errorf("second edit with the old updated_at = %d %s, want 409", code, body)
	}
	if code, _ := do("PUT", path, map[string]any{"name": "Edited", "source_languages": []string{"en"}, "definition": httpDef}); code != http.StatusBadRequest {
		t.Errorf("edit without updated_at = %d, want 400", code)
	}

	// Soft delete hides it.
	if code, _ := do("DELETE", path, nil); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _ := do("GET", path, nil); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
	var deletedAt *string
	if err := pool.QueryRow(t.Context(), `SELECT deleted_at::text FROM segmentation_profiles WHERE id = $1`, created.Data.ID).Scan(&deletedAt); err != nil || deletedAt == nil {
		t.Errorf("row after delete: deleted_at %v (%v), want set — a soft delete", deletedAt, err)
	}
}

func TestIntegrationSegmentationHTTP_PreviewAndSRX(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SegHTTPPreview")
	owner := testutil.SeedUser(t, q, tenant.ID, "o@seg-preview.test", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles/preview", map[string]any{
		"rule_set": map[string]any{
			"rules":         []map[string]any{{"before": `\?`, "after": `\s`, "break": false}, {"before": `[.?]`, "after": `\s`, "break": true}},
			"abbreviations": []string{"Dr"},
		},
		"text": "Dr. Who? Yes. Done.",
	}, owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var preview struct {
		Data struct {
			Segments []string `json:"segments"`
			Pieces   []struct {
				Text     string `json:"text"`
				Decision *struct {
					Break  bool   `json:"break"`
					Source string `json:"source"`
					Rule   *int   `json:"rule"`
					Word   string `json:"word"`
				} `json:"decision"`
			} `json:"pieces"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &preview)
	p := preview.Data
	if strings.Join(p.Segments, "|") != "Dr. Who? Yes.|Done." {
		t.Errorf("segments = %q", p.Segments)
	}
	if len(p.Pieces) != 4 || p.Pieces[0].Text != "Dr." || p.Pieces[0].Decision.Source != "abbreviation" || p.Pieces[0].Decision.Word != "Dr" ||
		*p.Pieces[1].Decision.Rule != 1 || p.Pieces[1].Decision.Break ||
		*p.Pieces[2].Decision.Rule != 2 || !p.Pieces[2].Decision.Break || p.Pieces[3].Decision != nil || p.Pieces[3].Text != " Done." {
		t.Errorf("pieces = %+v", p.Pieces)
	}
	svc := segmentationapi.NewService(q, pool)
	rec = testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles/preview", map[string]any{"id": builtinID(t, svc, tenant.ID, "builtin:en@1"), "text": strings.Repeat("a", 20001)}, owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// SRX upload: drafts and warnings, nothing stored.
	srx, err := os.ReadFile("../segmentation/testdata/srx/cascade.srx")
	if err != nil {
		t.Fatal(err)
	}
	rec = testutil.DoFileUpload(t, router, "/api/segmentation-profiles/srx/parse", "rules.srx", "application/xml", string(srx), owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, `"name":"English"`) || !strings.Contains(body, "look-behind is not supported") {
		t.Errorf("SRX parse = %s", body)
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM segmentation_profiles WHERE tenant_id = $1`, tenant.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("profiles after an SRX parse = %d (%v), want none stored", n, err)
	}
	rec = testutil.DoFileUpload(t, router, "/api/segmentation-profiles/srx/parse", "bad.srx", "application/xml", "<srx", owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoFileUpload(t, router, "/api/segmentation-profiles/srx/parse", "big.srx", "application/xml", strings.Repeat("x", 2<<20), owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// SRX download.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/segmentation-profiles/%d/srx", builtinID(t, svc, tenant.ID, "builtin:de@1")), nil, owner.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("content type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "German.srx") {
		t.Errorf("content disposition = %q", cd)
	}
	if !strings.Contains(rec.Body.String(), `<languagemap languagepattern="(?i)de(?:[-_].*)?" languagerulename="German"/>`) {
		t.Errorf("SRX body lacks the language map:\n%s", rec.Body.String())
	}
}

func TestIntegrationSegmentationHTTP_PreviewIsRateLimitedPerUser(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SegPreviewLimit")
	a := testutil.SeedUser(t, q, tenant.ID, "a@seg-limit.test", "pass")
	b := testutil.SeedUser(t, q, tenant.ID, "b@seg-limit.test", "pass")
	body := map[string]any{"rule_set": httpDef, "text": "One. Two."}
	limited := 0
	for range 30 {
		rec := testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles/preview", body, a.ID, tenant.ID)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("30 previews at once were all served, want the burst limited")
	}
	rec := testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles/preview", body, b.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// The sample-text limit counts characters, not bytes, and rules that take too
// long on the sample are a client error with a message, not a server error.
func TestIntegrationSegmentationHTTP_PreviewLimitsAreClientErrors(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "SegPreviewErrors")
	owner := testutil.SeedUser(t, q, tenant.ID, "o@seg-preview-errors.test", "pass")
	preview := func(rules any, text string) *httptest.ResponseRecorder {
		return testutil.DoRequest(t, router, "POST", "/api/segmentation-profiles/preview", map[string]any{"rule_set": rules, "text": text}, owner.ID, tenant.ID)
	}

	full := strings.Repeat("é", segmentation.MaxPreviewText)
	testutil.AssertStatus(t, preview(httpDef, full), http.StatusOK)
	rec := preview(httpDef, full+"é")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("at most %d characters", segmentation.MaxPreviewText)) {
		t.Errorf("over the limit: body = %s", rec.Body.String())
	}

	rec = preview(testutil.CostlySegmentationRules(), testutil.CostlySegmentationText)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "take too long to run on the sample text") {
		t.Errorf("costly rules: body = %s", rec.Body.String())
	}
}
