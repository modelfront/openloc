package spellcheck_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// LanguageTool is stubbed throughout. Its real output depends on the deployed
// rule set and dictionary version, so asserting against the live service would
// make these tests fail for reasons that have nothing to do with our code.

// ltStub serves a canned /v2/check response. `flag` is the exact substring to
// report as misspelled; offsets are computed in UTF-16 code units, matching
// what real LanguageTool (a Java service) returns.
func ltStub(t *testing.T, flag, suggestion string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		text := r.Form.Get("text")
		matches := []map[string]any{}
		if idx := utf16Index(text, flag); idx >= 0 {
			matches = append(matches, map[string]any{
				"message": "Possible spelling mistake found.",
				"offset":  idx,
				"length":  utf16Len(flag),
				"replacements": []map[string]string{
					{"value": suggestion},
				},
				"rule": map[string]any{
					"id":          "GERMAN_SPELLER_RULE",
					"description": "Possible typo",
					"category":    map[string]string{"id": "TYPOS", "name": "Possible Typo"},
				},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	}))
}

// utf16Index returns the index of `needle` in `haystack` measured in UTF-16
// code units — the unit LanguageTool reports offsets in.
func utf16Index(haystack, needle string) int {
	byteIdx := strings.Index(haystack, needle)
	if byteIdx < 0 {
		return -1
	}
	return utf16Len(haystack[:byteIdx])
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

type spellcheckJobResponse struct {
	Data struct {
		Results []struct {
			TranslationID int64 `json:"translation_id"`
			Matches       []struct {
				Offset       int `json:"offset"`
				Length       int `json:"length"`
				Replacements []struct {
					Value string `json:"value"`
				} `json:"replacements"`
			} `json:"matches"`
		} `json:"results"`
		Failed int `json:"failed"`
	} `json:"data"`
}

// scenario wires a tenant → project → file → job with one German translation
// whose text is `text`, and returns everything a test needs to call the API.
type scenario struct {
	router      http.Handler
	q           db.Querier
	userID      int64
	tenantID    int64
	projectID   int64
	profileID   int64
	fileID      int64
	jobID       int64
	translation db.Translation
}

func setupScenario(t *testing.T, ltURL, text string) scenario {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	router := testutil.NewTestRouterWithSpellcheck(pool, nil, ltURL)
	userID, tenantID := testutil.RegisterUser(t, q, "spellcheck")
	project := testutil.SeedProject(t, q, tenantID, "Spellcheck Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "doc.docx", db.FileFormatDocx)
	segment := testutil.SeedSegment(t, q, file.ID, "Send it to the address below.", 1)
	tr := testutil.SeedTranslation(t, q, segment.ID, "de", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "de")

	updated, err := q.SaveTranslationDraft(testutil.Ctx(), db.SaveTranslationDraftParams{
		ID:        tr.ID,
		HumanText: &text,
		EditedBy:  &userID,
	})
	if err != nil {
		t.Fatalf("seed translation text: %v", err)
	}

	return scenario{
		router: router, q: q, userID: userID, tenantID: tenantID,
		projectID: project.ID, profileID: project.SettingsProfileID,
		fileID: file.ID, jobID: job.ID, translation: updated,
	}
}

func (s scenario) runCheck(t *testing.T) spellcheckJobResponse {
	t.Helper()
	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/jobs/%d/spellcheck", s.jobID),
		map[string]any{"translation_ids": []int64{s.translation.ID}},
		s.userID, s.tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("spellcheck: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp spellcheckJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return resp
}

func TestIntegrationSpellcheckJob_ReturnsMatches(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	resp := s.runCheck(t)
	if len(resp.Data.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(resp.Data.Results))
	}
	got := resp.Data.Results[0]
	if got.TranslationID != s.translation.ID {
		t.Errorf("translation_id = %d, want %d", got.TranslationID, s.translation.ID)
	}
	if len(got.Matches) != 1 || got.Matches[0].Replacements[0].Value != "Adresse" {
		t.Errorf("unexpected matches: %+v", got.Matches)
	}
	// Offset must point at "adresse" so a Change lands on the right characters.
	if want := 21; got.Matches[0].Offset != want {
		t.Errorf("offset = %d, want %d", got.Matches[0].Offset, want)
	}
}

// The offsets LanguageTool returns are UTF-16 code units. Any byte-based
// handling of them breaks the moment a segment contains an umlaut — which is
// most German target text — so the dictionary filter is exercised against a
// multi-byte segment specifically.
func TestIntegrationSpellcheckJob_DictionaryFiltersMultibyteText(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	// Umlauts before the flagged word push the byte offset past the UTF-16 one.
	s := setupScenario(t, lt.URL, "Die Prüfung enthält die adresse dort.")

	if got := len(s.runCheck(t).Data.Results); got != 1 {
		t.Fatalf("precondition: want 1 result before ignoring, got %d", got)
	}

	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", s.projectID),
		map[string]any{"language": "de", "word": "adresse"}, s.userID, s.tenantID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dictionary word: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := len(s.runCheck(t).Data.Results); got != 0 {
		t.Errorf("after ignoring 'adresse', want 0 results, got %d", got)
	}
}

// Case matters: in German `adresse` vs `Adresse` IS the error, so a dictionary
// entry for the correctly-cased word must not suppress the misspelled one.
func TestIntegrationSpellcheckJob_DictionaryIsCaseSensitive(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", s.projectID),
		map[string]any{"language": "de", "word": "Adresse"}, s.userID, s.tenantID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dictionary word: want 201, got %d", rec.Code)
	}

	if got := len(s.runCheck(t).Data.Results); got != 1 {
		t.Errorf("capitalised dictionary entry must not suppress the lowercase typo; got %d results", got)
	}
}

func TestIntegrationSpellcheckJob_TenantIsolation(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	otherUser, otherTenant := testutil.RegisterUser(t, s.q, "spellcheck-outsider")
	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/jobs/%d/spellcheck", s.jobID),
		map[string]any{"translation_ids": []int64{s.translation.ID}},
		otherUser, otherTenant)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant spellcheck: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Pins that a LanguageTool outage is counted in `failed`, not silently
// swallowed.
func TestIntegrationSpellcheckJob_ReportsUpstreamFailures(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer broken.Close()
	s := setupScenario(t, broken.URL, "Senden Sie es an die adresse unten.")

	resp := s.runCheck(t)
	if resp.Data.Failed != 1 {
		t.Errorf("failed = %d, want 1 so the UI can tell the user", resp.Data.Failed)
	}
	if len(resp.Data.Results) != 0 {
		t.Errorf("want no results from a failed check, got %d", len(resp.Data.Results))
	}
}

func TestIntegrationSpellcheck_RequiresAuth(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouterWithSpellcheck(pool, nil, "http://unused.invalid")

	body := strings.NewReader(url.Values{}.Encode())
	req := httptest.NewRequest(http.MethodPost, "/api/spellcheck", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /api/spellcheck: want 401, got %d", rec.Code)
	}
}

func TestIntegrationSpellcheckDictionary_DeleteRequiresAdmin(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", s.projectID),
		map[string]any{"language": "de", "word": "Acmecorp"}, s.userID, s.tenantID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data db.SpellcheckDictionary `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	hasWord := func() bool {
		for _, e := range s.listDictionary(t, "de") {
			if e.ID == created.Data.ID && e.Word == "Acmecorp" {
				return true
			}
		}
		return false
	}
	if !hasWord() {
		t.Fatal("added word missing from the dictionary before the delete")
	}

	// A project manager passes the project group's manager gate, so this
	// 403 comes from the route's own owner/admin gate.
	path := fmt.Sprintf("/api/projects/%d/spellcheck-dictionary/%d", s.projectID, created.Data.ID)
	if rec := testutil.DoRequestWithRole(t, s.router, http.MethodDelete, path, nil,
		s.userID, s.tenantID, "project_manager"); rec.Code != http.StatusForbidden {
		t.Errorf("project manager delete: want 403, got %d", rec.Code)
	}
	if !hasWord() {
		t.Fatal("a refused delete removed the word")
	}
	if rec := testutil.DoRequestWithRole(t, s.router, http.MethodDelete, path, nil,
		s.userID, s.tenantID, "admin"); rec.Code != http.StatusNoContent {
		t.Errorf("admin delete: want 204, got %d", rec.Code)
	}
	if hasWord() {
		t.Error("admin delete returned 204 but the word is still in the dictionary")
	}
}

func (s scenario) listDictionary(t *testing.T, lang string) []db.SpellcheckDictionary {
	t.Helper()
	rec := testutil.DoRequest(t, s.router, http.MethodGet,
		fmt.Sprintf("/api/projects/%d/spellcheck-dictionary?language=%s", s.projectID, lang), nil, s.userID, s.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []db.SpellcheckDictionary `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return listed.Data
}

// Adding the same word twice is what happens when two linguists both hit
// "Ignore all" on the same brand name. It must not 500.
func TestIntegrationSpellcheckDictionary_AddIsIdempotent(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	path := fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", s.projectID)
	body := map[string]any{"language": "de", "word": "Nordwind"}
	for i := 0; i < 2; i++ {
		rec := testutil.DoRequest(t, s.router, http.MethodPost, path, body, s.userID, s.tenantID)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add #%d: want 201, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	if got := s.listDictionary(t, "de"); len(got) != 1 {
		t.Errorf("want 1 dictionary entry after a duplicate add, got %d", len(got))
	}
}

// --- Live single-segment endpoint (the inline underlines) ---

type spellcheckLiveResponse struct {
	Matches []struct {
		Offset       int `json:"offset"`
		Replacements []struct {
			Value string `json:"value"`
		} `json:"replacements"`
	} `json:"matches"`
}

func (s scenario) runLiveCheck(t *testing.T, body map[string]any, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequest(t, s.router, http.MethodPost, "/api/spellcheck", body, userID, tenantID)
}

func decodeLive(t *testing.T, rec *httptest.ResponseRecorder) spellcheckLiveResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("live spellcheck: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp spellcheckLiveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return resp
}

// The inline underlines and the review dialog must agree on what counts as an
// error. A word ignored in the project dictionary from either surface has to
// stop being flagged by the live check too, or the squiggle outlives the
// decision.
func TestIntegrationSpellcheckLive_JobScopesDictionary(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Die Prüfung enthält die adresse dort.")
	body := map[string]any{"text": "Die Prüfung enthält die adresse dort.", "language": "de", "job_id": s.jobID}

	if got := len(decodeLive(t, s.runLiveCheck(t, body, s.userID, s.tenantID)).Matches); got != 1 {
		t.Fatalf("precondition: want 1 match before ignoring, got %d", got)
	}

	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/projects/%d/spellcheck-dictionary", s.projectID),
		map[string]any{"language": "de", "word": "adresse"}, s.userID, s.tenantID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dictionary word: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := len(decodeLive(t, s.runLiveCheck(t, body, s.userID, s.tenantID)).Matches); got != 0 {
		t.Errorf("after ignoring 'adresse' in the project, the live check still flags it (%d matches)", got)
	}

	// Without a job the endpoint has no project to scope by and reports raw
	// LanguageTool output — that's the documented contract, not a leak.
	noJob := map[string]any{"text": "Die Prüfung enthält die adresse dort.", "language": "de"}
	if got := len(decodeLive(t, s.runLiveCheck(t, noJob, s.userID, s.tenantID)).Matches); got != 1 {
		t.Errorf("without job_id, want raw LanguageTool result (1 match), got %d", got)
	}
}

// A job id from the client is only a hint until it's verified against the
// caller's tenant. Another tenant naming our job must get a 404, not our
// dictionary applied to their text.
func TestIntegrationSpellcheckLive_JobTenantIsolation(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	otherUser, otherTenant := testutil.RegisterUser(t, s.q, "spellcheck-outsider")
	rec := s.runLiveCheck(t, map[string]any{
		"text": "die adresse", "language": "de", "job_id": s.jobID,
	}, otherUser, otherTenant)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant live spellcheck with job_id: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Linguists reach the CAT editor only for jobs assigned to them, and
// every /api/jobs/{id}/* route enforces that. Naming a job on the live check
// must be held to the same rule, or an unassigned linguist can probe any job in
// the tenant (exists? is this word in its dictionary?) through the side door.
func TestIntegrationSpellcheckLive_LinguistNeedsJobAssignment(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")
	linguist := testutil.SeedUserWithRole(t, s.q, s.tenantID, "linguist@spellcheck.test", "pw", "linguist")
	body := map[string]any{"text": "die adresse", "language": "de", "job_id": s.jobID}

	rec := testutil.DoRequestWithRole(t, s.router, http.MethodPost, "/api/spellcheck", body, linguist.ID, s.tenantID, "linguist")
	if rec.Code != http.StatusForbidden {
		t.Errorf("unassigned linguist naming a job: want 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Without a job there is nothing to be assigned to; the plain check stays open.
	rec = testutil.DoRequestWithRole(t, s.router, http.MethodPost, "/api/spellcheck",
		map[string]any{"text": "die adresse", "language": "de"}, linguist.ID, s.tenantID, "linguist")
	if rec.Code != http.StatusOK {
		t.Errorf("linguist live check without job_id: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	testutil.SeedJobStep(t, s.q, s.profileID, s.jobID, &linguist.ID)
	rec = testutil.DoRequestWithRole(t, s.router, http.MethodPost, "/api/spellcheck", body, linguist.ID, s.tenantID, "linguist")
	if got := len(decodeLive(t, rec).Matches); got != 1 {
		t.Errorf("assigned linguist: want 1 match, got %d", got)
	}
}
