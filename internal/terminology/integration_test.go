package terminology_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/terminology"
	"openloc/internal/testutil"
)

// mustCreateEntry inserts a bilingual concept (base source-language term + a
// target-language term) the same way the handlers do (CreateConcept).
func mustCreateEntry(t *testing.T, q db.Querier, base db.TermBasis, src, tgt, tgtLang, status string) {
	t.Helper()
	ci := terminology.ConceptInput{Terms: []terminology.ConceptTermInput{
		{Language: base.SourceLanguage, Text: src, Status: "approved"},
		{Language: tgtLang, Text: tgt, Status: status},
	}}
	if _, err := terminology.CreateConcept(context.Background(), q, base, ci, nil); err != nil {
		t.Fatalf("create term %q→%q: %v", src, tgt, err)
	}
}

func TestIntegrationTermBaseCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Tech Term Base",
		"source_language": "en",
		"target_language": "es",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var createResp struct {
		Data struct {
			ID             int64  `json:"id"`
			Name           string `json:"name"`
			SourceLanguage string `json:"source_language"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	baseID := createResp.Data.ID

	if createResp.Data.Name != "Tech Term Base" {
		t.Errorf("expected 'Tech Term Base', got %q", createResp.Data.Name)
	}
	if createResp.Data.SourceLanguage != "en" {
		t.Errorf("expected source 'en', got %q", createResp.Data.SourceLanguage)
	}

	rec = testutil.DoRequest(t, router, "GET", "/api/terminology", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct {
			ID         int64 `json:"id"`
			EntryCount int64 `json:"entry_count"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 base, got %d", len(listResp.Data))
	}
	if listResp.Data[0].EntryCount != 0 {
		t.Errorf("expected 0 entries, got %d", listResp.Data[0].EntryCount)
	}

	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/terminology/%d", baseID), map[string]string{
		"name":        "Updated Term Base",
		"description": "Updated description",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Verify deleted — list should be empty
	rec = testutil.DoRequest(t, router, "GET", "/api/terminology", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("expected 0 bases after delete, got %d", len(listResp.Data))
	}
}

// Deleting a term base from the UI SOFT-deletes it and cascades to its concepts
// and terms: the base disappears from the list + 404s on access, but the rows
// remain in the DB with deleted_at set (audit/restore, and fast vs. a hard cascade).
func TestIntegrationTermBaseSoftDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Soft", "source_language": "en", "target_language": "de",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// A concept with two terms.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
		"definition": "d",
		"terms": []map[string]string{
			{"language": "en", "text": "hello"},
			{"language": "de", "text": "hallo"},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Delete the base.
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Gone from the list, and access 404s (GetTermBase excludes soft-deleted).
	rec = testutil.DoRequest(t, router, "GET", "/api/terminology", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	for _, b := range listResp.Data {
		if b.ID == baseID {
			t.Error("soft-deleted base still appears in the list")
		}
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// SOFT: the rows are still there, with deleted_at set (not hard-deleted).
	ctx := context.Background()
	var baseDeleted bool
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM term_bases WHERE id = $1`, baseID).Scan(&baseDeleted); err != nil {
		t.Fatalf("term base row should still exist: %v", err)
	}
	if !baseDeleted {
		t.Error("term base deleted_at not set")
	}
	var liveConcepts, liveTerms int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM term_concept WHERE term_base_id = $1 AND deleted_at IS NULL`, baseID).Scan(&liveConcepts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM term t JOIN term_concept c ON c.id = t.concept_id WHERE c.term_base_id = $1 AND t.deleted_at IS NULL`, baseID).Scan(&liveTerms); err != nil {
		t.Fatal(err)
	}
	if liveConcepts != 0 || liveTerms != 0 {
		t.Errorf("cascade soft-delete incomplete: live concepts=%d terms=%d (want 0/0)", liveConcepts, liveTerms)
	}
	// But the rows physically remain (soft, not hard).
	var totalConcepts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM term_concept WHERE term_base_id = $1`, baseID).Scan(&totalConcepts); err != nil {
		t.Fatal(err)
	}
	if totalConcepts != 1 {
		t.Errorf("concept row should be retained (soft delete), got %d", totalConcepts)
	}
}

func TestIntegrationTermBaseOwnership(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant1 := testutil.SeedTenant(t, q, "Corp1")
	user1 := testutil.SeedUser(t, q, tenant1.ID, "user1@test.com", "pass")
	tenant2 := testutil.SeedTenant(t, q, "Corp2")
	user2 := testutil.SeedUser(t, q, tenant2.ID, "user2@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name":            "Private Term Base",
		"source_language": "en",
		"target_language": "fr",
	}, user1.ID, tenant1.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	// User2 cannot see it
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d", resp.Data.ID), nil, user2.ID, tenant2.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// User2 cannot delete it
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d", resp.Data.ID), nil, user2.ID, tenant2.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationCSVImportExport(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "CSV Test", "source_language": "en", "target_language": "es",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// Build CSV multipart upload
	csvContent := "source_term,target_term,status,notes,domain\n" +
		"hello,hola,approved,greeting,general\n" +
		"goodbye,adiós,approved,,\n" +
		"bad,malo,forbidden,,\n" +
		",,,,\n" // should be skipped (empty terms)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "terms.csv")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(csvContent))
	writer.Close()

	// A multilingual base has no default target, so bilingual CSV import needs an
	// explicit target language (?target=es here; per-row target_language columns
	// also work).
	rec = testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import?target=es", baseID),
		&buf, writer.FormDataContentType(), user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var importResp struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	testutil.DecodeJSON(t, rec, &importResp)
	if importResp.Imported != 3 {
		t.Errorf("expected 3 imported, got %d", importResp.Imported)
	}
	if importResp.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", importResp.Skipped)
	}

	// Export CSV, single target — compact 6-column format (source_language first,
	// notes last) that CSV import round-trips.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=es", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("source_language,source_term,target_term,status,domain,notes")) {
		t.Errorf("bilingual export missing 6-column header w/ source_language; body: %s", body[:min(200, len(body))])
	}
	if !bytes.Contains([]byte(body), []byte("en,hello,hola,approved,general,greeting")) {
		t.Errorf("bilingual export missing source_language + entry; body: %s", body[:min(200, len(body))])
	}
	if rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Errorf("expected CSV content type, got %q", rec.Header().Get("Content-Type"))
	}

	// Export CSV, no target — all-languages mode adds a target_language column
	// (source_language first, notes last).
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	body = rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("source_language,source_term,target_language,target_term,status,domain,notes")) {
		t.Errorf("all-languages export missing 7-column header w/ source_language; body: %s", body[:min(200, len(body))])
	}
	if !bytes.Contains([]byte(body), []byte("en,hello,es,hola,approved,general,greeting")) {
		t.Errorf("all-languages export missing source_language/target_language columns; body: %s", body[:min(200, len(body))])
	}
}

// Concept-centric CRUD. Exercises create/list/get/update/delete of a
// multilingual concept and its terms through the HTTP handlers.
func TestIntegrationConceptCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Concepts", "source_language": "en", "target_language": "es",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	type termDTO struct {
		ID           int64  `json:"id"`
		Language     string `json:"language"`
		Text         string `json:"text"`
		Status       string `json:"status"`
		PartOfSpeech string `json:"part_of_speech"`
		Notes        string `json:"notes"`
		CreatedAt    string `json:"created_at"`
		CreatedBy    string `json:"created_by"`
		UpdatedBy    string `json:"updated_by"`
	}
	type ConceptDTO struct {
		ID         int64     `json:"id"`
		Definition string    `json:"definition"`
		Domain     string    `json:"domain"`
		Terms      []termDTO `json:"terms"`
	}

	// Create a multilingual concept: en + two es synonyms + fr.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
		"definition": "a greeting",
		"domain":     "general",
		"terms": []map[string]string{
			{"language": "en", "text": "hello", "status": "approved"},
			{"language": "es", "text": "hola", "status": "approved"},
			{"language": "es", "text": "buenas", "status": "forbidden"},
			{"language": "FR", "text": "bonjour", "status": "approved", "part_of_speech": "interjection"},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data ConceptDTO `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	conceptID := created.Data.ID
	if conceptID == 0 {
		t.Fatal("created concept has no id")
	}

	// GET the concept back: 4 terms, definition/domain preserved.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got struct {
		Data ConceptDTO `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Definition != "a greeting" || got.Data.Domain != "general" {
		t.Errorf("definition/domain not preserved: %+v", got.Data)
	}
	if len(got.Data.Terms) != 4 {
		t.Fatalf("expected 4 terms, got %d", len(got.Data.Terms))
	}
	// The create answered the concept as stored: the same term ids and
	// normalized codes the GET returns, not the request echoed back.
	if !reflect.DeepEqual(created.Data.Terms, got.Data.Terms) {
		t.Errorf("create answered terms %+v, stored %+v", created.Data.Terms, got.Data.Terms)
	}
	var forbidden, fr *termDTO
	for i := range got.Data.Terms {
		switch got.Data.Terms[i].Text {
		case "buenas":
			forbidden = &got.Data.Terms[i]
		case "bonjour":
			fr = &got.Data.Terms[i]
		}
	}
	if forbidden == nil || forbidden.Status != "forbidden" {
		t.Errorf("es 'buenas' should be forbidden; got %+v", forbidden)
	}
	if fr == nil || fr.PartOfSpeech != "interjection" || fr.Language != "fr" {
		t.Errorf("fr 'bonjour' should carry part_of_speech and the normalized code; got %+v", fr)
	}
	// Per-term audit stamps: the creating user's name + a timestamp are surfaced
	// (they back the concept dialog's term stamps).
	if fr != nil && (fr.CreatedBy != user.Name || fr.CreatedAt == "") {
		t.Errorf("fr term should carry created_by=%q + created_at; got created_by=%q created_at=%q", user.Name, fr.CreatedBy, fr.CreatedAt)
	}

	// List: exactly this concept, with its terms.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data  []ConceptDTO `json:"data"`
		Total int64        `json:"total"`
	}
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 1 || len(list.Data) != 1 || len(list.Data[0].Terms) != 4 {
		t.Fatalf("list should have 1 concept with 4 terms; got total=%d data=%+v", list.Total, list.Data)
	}

	// Update concept definition/domain.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), map[string]string{
		"definition": "a friendly greeting", "domain": "chitchat",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Add a German term.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms", baseID, conceptID), map[string]string{
		"language": "de", "text": "hallo", "status": "approved",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var addedTerm struct {
		Data termDTO `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &addedTerm)

	// Update the fr term's status to forbidden.
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", baseID, conceptID, fr.ID), map[string]string{
		"language": "fr", "text": "bonjour", "status": "forbidden",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Delete the forbidden es synonym.
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", baseID, conceptID, forbidden.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// GET again: de added (5-1 deleted = 4 terms), fr forbidden, buenas gone.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &got)
	if got.Data.Definition != "a friendly greeting" || got.Data.Domain != "chitchat" {
		t.Errorf("concept update not persisted: %+v", got.Data)
	}
	texts := map[string]string{}
	for _, tm := range got.Data.Terms {
		texts[tm.Text] = tm.Status
	}
	if len(got.Data.Terms) != 4 {
		t.Errorf("expected 4 live terms after add+delete, got %d (%v)", len(got.Data.Terms), texts)
	}
	if _, ok := texts["buenas"]; ok {
		t.Error("deleted term 'buenas' still present")
	}
	if texts["hallo"] != "approved" {
		t.Error("added German term missing")
	}
	if texts["bonjour"] != "forbidden" {
		t.Errorf("fr term status not updated; got %q", texts["bonjour"])
	}

	// Delete the whole concept.
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 0 || len(list.Data) != 0 {
		t.Errorf("concept list should be empty after delete; got %+v", list)
	}
}

// The ?lang= filter narrows the concept list to concepts that have a term in
// the language, and collapses each returned concept to just that language's
// terms (the concept-browser / dialog language filter).
func TestIntegrationConceptLanguageFilter(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Concepts", "source_language": "en", "target_language": "es",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// Concept A: en + de. Concept B: en + fr (no de).
	mk := func(terms []map[string]string) {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
			"definition": "d", "terms": terms,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
	}
	mk([]map[string]string{{"language": "en", "text": "car"}, {"language": "de", "text": "Auto"}})
	mk([]map[string]string{{"language": "en", "text": "house"}, {"language": "fr", "text": "maison"}})

	type termDTO struct {
		Language string `json:"language"`
		Text     string `json:"text"`
	}
	type ConceptDTO struct {
		ID    int64     `json:"id"`
		Terms []termDTO `json:"terms"`
	}
	var list struct {
		Data  []ConceptDTO `json:"data"`
		Total int64        `json:"total"`
	}

	// No filter: both concepts, all terms.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 2 || len(list.Data) != 2 {
		t.Fatalf("unfiltered: want 2 concepts, got total=%d data=%d", list.Total, len(list.Data))
	}

	// ?lang=de: only concept A, and only its de term.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts?lang=de", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 1 || len(list.Data) != 1 {
		t.Fatalf("lang=de: want 1 concept, got total=%d data=%d", list.Total, len(list.Data))
	}
	if len(list.Data[0].Terms) != 1 || list.Data[0].Terms[0].Language != "de" || list.Data[0].Terms[0].Text != "Auto" {
		t.Errorf("lang=de: concept should be collapsed to its de term; got %+v", list.Data[0].Terms)
	}

	// ?lang=de&lang=fr (multi-select): both concepts, each collapsed to the
	// matching language's term (A→de, B→fr).
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts?lang=de&lang=fr", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 2 || len(list.Data) != 2 {
		t.Fatalf("lang=de,fr: want 2 concepts, got total=%d data=%d", list.Total, len(list.Data))
	}
	for _, c := range list.Data {
		if len(c.Terms) != 1 {
			t.Errorf("lang=de,fr: each concept should keep its one matching term; got %+v", c.Terms)
			continue
		}
		if l := c.Terms[0].Language; l != "de" && l != "fr" {
			t.Errorf("lang=de,fr: unexpected term language %q", l)
		}
	}

	// ?lang=it: no concept has Italian.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts?lang=it", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &list)
	if list.Total != 0 || len(list.Data) != 0 {
		t.Errorf("lang=it: want 0 concepts, got total=%d data=%d", list.Total, len(list.Data))
	}
}

// When a language filter is active, the term-text search must only match terms
// in the selected languages — searching for an English word while filtered to
// German should NOT surface a concept just because its English term matches.
func TestIntegrationConceptSearchScopedToLanguages(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Scoped", "source_language": "en",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	mk := func(terms []map[string]string) {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
			"terms": terms,
		}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
	}
	// Concept: en "widget" + de "Gerät". "widget" is ONLY the English term.
	mk([]map[string]string{{"language": "en", "text": "widget"}, {"language": "de", "text": "Gerät"}})

	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	get := func(qs string) {
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts?%s", baseID, qs), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		testutil.DecodeJSON(t, rec, &list)
	}

	// Search "widget" with NO language filter → the English term matches.
	get("q=widget")
	if list.Total != 1 {
		t.Fatalf("q=widget (no filter): want 1, got %d", list.Total)
	}
	// Search "widget" filtered to German → "widget" isn't a German term, so no
	// match even though the concept has other de terms.
	get("q=widget&lang=de")
	if list.Total != 0 {
		t.Errorf("q=widget&lang=de: search must be scoped to German terms; want 0, got %d", list.Total)
	}
	// Search the actual German term while filtered to German → matches.
	get("q=Gerät&lang=de")
	if list.Total != 1 {
		t.Errorf("q=Gerät&lang=de: want 1, got %d", list.Total)
	}
}

// A concept in one tenant's base must be invisible and immutable to another
// tenant — every concept/term op resolves the id only within the caller's base.
func TestIntegrationConceptTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	owner := testutil.SeedTenant(t, q, "Owner")
	ownerUser := testutil.SeedUser(t, q, owner.ID, "owner@test.com", "pass")
	intruder := testutil.SeedTenant(t, q, "Intruder")
	intruderUser := testutil.SeedUser(t, q, intruder.ID, "intruder@test.com", "pass")

	type concept struct {
		ID     int64  `json:"id"`
		Domain string `json:"domain"`
		Terms  []struct {
			ID     int64  `json:"id"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"terms"`
	}
	createBase := func(userID, tenantID int64) int64 {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
			"name": "Base", "source_language": "en", "target_language": "es",
		}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		return out.Data.ID
	}
	getConcept := func(baseID, conceptID, userID, tenantID int64) concept {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var out struct {
			Data concept `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		return out.Data
	}
	// createConcept returns the concept as read back, so its term ids are set.
	createConcept := func(baseID int64, domain, text string, userID, tenantID int64) concept {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
			"domain": domain,
			"terms":  []map[string]string{{"language": "en", "text": text}},
		}, userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var out struct {
			Data concept `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &out)
		c := getConcept(baseID, out.Data.ID, userID, tenantID)
		if len(c.Terms) != 1 || c.Terms[0].ID == 0 {
			t.Fatalf("create concept: want one term with an id, got %+v", c)
		}
		return c
	}

	baseID := createBase(ownerUser.ID, owner.ID)
	victim := createConcept(baseID, "secret", "classified", ownerUser.ID, owner.ID)
	conceptID, termID := victim.ID, victim.Terms[0].ID

	// The intruder's own base and concept pass the base and concept checks, so
	// these probes reach the per-concept and per-term lookups.
	intruderBaseID := createBase(intruderUser.ID, intruder.ID)
	intruderConceptID := createConcept(intruderBaseID, "mine", "own", intruderUser.ID, intruder.ID).ID

	hackTerm := map[string]string{"language": "en", "text": "hacked", "status": "forbidden"}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		// The victim's base.
		{"GET", fmt.Sprintf("/api/terminology/%d/concepts", baseID), nil},
		{"GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil},
		{"PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), map[string]string{"domain": "hacked"}},
		{"DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil},
		{"POST", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms", baseID, conceptID), map[string]string{"language": "es", "text": "x"}},
		{"PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", baseID, conceptID, termID), hackTerm},
		{"DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", baseID, conceptID, termID), nil},
		// The intruder's base with the victim's concept.
		{"GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", intruderBaseID, conceptID), nil},
		{"PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d", intruderBaseID, conceptID), map[string]string{"domain": "hacked"}},
		{"DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d", intruderBaseID, conceptID), nil},
		{"POST", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms", intruderBaseID, conceptID), map[string]string{"language": "es", "text": "x"}},
		{"PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", intruderBaseID, conceptID, termID), hackTerm},
		{"DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", intruderBaseID, conceptID, termID), nil},
		// The intruder's own concept with the victim's term.
		{"PATCH", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", intruderBaseID, intruderConceptID, termID), hackTerm},
		{"DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", intruderBaseID, intruderConceptID, termID), nil},
	} {
		rec := testutil.DoRequest(t, router, tc.method, tc.path, tc.body, intruderUser.ID, intruder.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: intruder got %d, want 404", tc.method, tc.path, rec.Code)
		}
	}

	// The owner's concept is unchanged.
	got := getConcept(baseID, conceptID, ownerUser.ID, owner.ID)
	if got.Domain != "secret" {
		t.Errorf("domain = %q, want %q", got.Domain, "secret")
	}
	if len(got.Terms) != 1 || got.Terms[0] != victim.Terms[0] {
		t.Errorf("terms changed: got %+v, want only %+v", got.Terms, victim.Terms[0])
	}
}

// A per-row target_language column (the all-languages export format) imports each
// row into its own target language — not the base's pinned target — and a plain
// bilingual CSV honors a ?target= override. Together these let a multilingual
// base import any pair and an all-languages export round-trip.
func TestIntegrationCSVImportTargetLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Multi", "source_language": "en", "target_language": "es",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	uploadCSV := func(path, content string) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, err := writer.CreateFormFile("file", "terms.csv")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(content))
		writer.Close()
		rec = testutil.DoRequestRaw(t, router, "POST", path, &buf, writer.FormDataContentType(), user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	// All-languages format: each row states its own target language.
	uploadCSV(fmt.Sprintf("/api/terminology/%d/import", baseID),
		"source_term,target_language,target_term,status,domain,notes\n"+
			"hello,de,hallo,approved,,\n"+
			"hello,fr,bonjour,approved,,\n"+
			"world,es,mundo,approved,,\n")

	// The languages present must include de and fr, not just the base en→es pair.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/languages", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var langResp struct {
		Data []string `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &langResp)
	if got := strings.Join(langResp.Data, ","); got != "de,en,es,fr" {
		t.Errorf("expected languages de,en,es,fr after multilingual import, got %v", langResp.Data)
	}

	// Round-trip: an all-languages export must reproduce the de/fr rows.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	csvBody := rec.Body.String()
	for _, want := range []string{"hello,de,hallo", "hello,fr,bonjour", "world,es,mundo"} {
		if !bytes.Contains([]byte(csvBody), []byte(want)) {
			t.Errorf("round-trip export missing %q; body: %s", want, csvBody)
		}
	}

	// A plain bilingual CSV imported with ?target=fr lands in French, not es.
	uploadCSV(fmt.Sprintf("/api/terminology/%d/import?target=fr", baseID),
		"source_term,target_term,status\ngoodbye,au revoir,approved\n")
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=fr", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	csvBody = rec.Body.String()
	if !bytes.Contains([]byte(csvBody), []byte("goodbye,au revoir")) {
		t.Errorf("?target=fr import did not land in French; fr export: %s", csvBody)
	}
	// And it must NOT have leaked into the base's es target.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=es", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if bytes.Contains([]byte(rec.Body.String()), []byte("goodbye")) {
		t.Errorf("?target=fr import leaked into es export")
	}
}

func TestIntegrationTBXImportExport(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Create en→fr base
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "TBX Test", "source_language": "en", "target_language": "fr",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// TBX-Basic v3 payload: c1 has an approved + a deprecated fr term and a
	// concept-level subjectField; c2 is en+de (stored as a multilingual concept,
	// but invisible in the en→fr default view).
	tbxContent := `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
  <text><body>
    <conceptEntry id="c1">
      <descrip type="subjectField">general</descrip>
      <langSec xml:lang="en"><termSec><term>hello</term></termSec></langSec>
      <langSec xml:lang="fr">
        <termSec><term>bonjour</term><termNote type="administrativeStatus">preferredTerm-admn-sts</termNote></termSec>
        <termSec><term>salut</term><termNote type="administrativeStatus">deprecatedTerm-admn-sts</termNote></termSec>
      </langSec>
    </conceptEntry>
    <conceptEntry id="c2">
      <langSec xml:lang="en"><termSec><term>orphan</term></termSec></langSec>
      <langSec xml:lang="de"><termSec><term>Waise</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "terms.tbx")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(tbxContent))
	writer.Close()

	rec = testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import-tbx", baseID),
		&buf, writer.FormDataContentType(), user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var importResp struct {
		Imported int `json:"imported"` // concepts
		Terms    int `json:"terms"`
		Skipped  int `json:"skipped"`
	}
	testutil.DecodeJSON(t, rec, &importResp)
	if importResp.Imported != 2 { // 2 concepts (c1 hello, c2 orphan)
		t.Errorf("expected 2 concepts imported, got %d", importResp.Imported)
	}
	if importResp.Terms != 5 { // c1: hello + bonjour + salut; c2: orphan + Waise
		t.Errorf("expected 5 terms across concepts, got %d", importResp.Terms)
	}

	// Verify the mapped rows: status forbidden for the deprecated term, domain from subjectField.
	ctx := context.Background()
	rows, err := q.ListConceptTermsForExport(ctx, baseID)
	if err != nil {
		t.Fatal(err)
	}
	var sawForbidden, sawDomain bool
	for _, r := range rows {
		if r.Text == "salut" && r.Status == "forbidden" {
			sawForbidden = true
		}
		if r.Text == "bonjour" && r.Domain == "general" {
			sawDomain = true
		}
	}
	if !sawForbidden {
		t.Error("expected 'salut' to map to forbidden status")
	}
	if !sawDomain {
		t.Error("expected 'bonjour' to inherit concept-level domain 'general'")
	}

	// Export TBX and confirm it is well-formed TBX-Basic containing the terms.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export-tbx", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("expected XML content type, got %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`type="TBX-Basic"`, "<term>hello</term>", "<term>bonjour</term>", "deprecatedTerm-admn-sts"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("export TBX missing %q", want)
		}
	}

	// All-languages CSV export must surface the German term the en→fr view hides.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	csvBody := rec.Body.String()
	for _, want := range []string{"hello,fr,bonjour", "hello,fr,salut", "orphan,de,Waise"} {
		if !bytes.Contains([]byte(csvBody), []byte(want)) {
			t.Errorf("all-languages CSV export missing %q; body: %s", want, csvBody)
		}
	}

	// A bilingual export to de reaches the concept the default view cannot.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=de", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	csvBody = rec.Body.String()
	if !bytes.Contains([]byte(csvBody), []byte("orphan,Waise")) {
		t.Errorf("de-target export missing 'orphan,Waise'; body: %s", csvBody)
	}
	if bytes.Contains([]byte(csvBody), []byte("bonjour")) {
		t.Errorf("de-target export should not contain fr terms; body: %s", csvBody)
	}

	// Multi-target export (fr + de): includes the target_language column and rows
	// for both languages, but nothing else.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/export?target=fr&target=de", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	csvBody = rec.Body.String()
	if !bytes.Contains([]byte(csvBody), []byte("source_language,source_term,target_language,target_term,status,domain,notes")) {
		t.Errorf("multi-target export missing 7-column header w/ source_language; body: %s", csvBody)
	}
	for _, want := range []string{"hello,fr,bonjour", "orphan,de,Waise"} {
		if !bytes.Contains([]byte(csvBody), []byte(want)) {
			t.Errorf("multi-target export missing %q; body: %s", want, csvBody)
		}
	}

	// The languages endpoint feeds the export target picker; it must list every
	// language present (de/en/fr), not just the base's default en→fr pair.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/languages", baseID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var langResp struct {
		Data []string `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &langResp)
	got := strings.Join(langResp.Data, ",")
	if got != "de,en,fr" {
		t.Errorf("expected languages [de en fr] (sorted), got %v", langResp.Data)
	}
}

// A large import crosses the transaction-batch boundary (importBatchSize)
// without dropping concepts.
func TestIntegrationTBXImportBatchedLarge(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Big TBX", "source_language": "en", "target_language": "de",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// 550 concepts: > importBatchSize (200), so it spans 3 batches.
	const n = 550
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<martif type="TBX-Basic" xml:lang="en-US"><text><body>` + "\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `<termEntry id="%d"><langSet xml:lang="en-US"><tig><term>term%d</term></tig></langSet>`+
			`<langSet xml:lang="de-DE"><tig><term>Begriff%d</term></tig></langSet></termEntry>`+"\n", i, i, i)
	}
	sb.WriteString(`</body></text></martif>`)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "big.tbx")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(sb.String()))
	writer.Close()

	rec = testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import-tbx", baseID),
		&buf, writer.FormDataContentType(), user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Imported int `json:"imported"`
		Terms    int `json:"terms"`
		Skipped  int `json:"skipped"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Imported != n {
		t.Errorf("expected all %d concepts imported (no loss across batch boundaries), got %d (skipped %d)", n, resp.Imported, resp.Skipped)
	}
	if resp.Terms != n*2 {
		t.Errorf("expected %d terms, got %d", n*2, resp.Terms)
	}

	// Confirm the rows actually landed (count matches).
	count, err := q.CountConcepts(context.Background(), db.CountConceptsParams{TermBaseID: baseID})
	if err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Errorf("DB has %d live concepts, want %d", count, n)
	}
}

func TestIntegrationTBXImportOtherLanguages(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Base is en→fr, but the file only carries es/de terms → nothing to import.
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "TBX Mismatch", "source_language": "en", "target_language": "fr",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	tbxContent := `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="es" xmlns="urn:iso:std:iso:30042:ed-2">
  <text><body>
    <conceptEntry id="c1">
      <langSec xml:lang="es"><termSec><term>hola</term></termSec></langSec>
      <langSec xml:lang="de"><termSec><term>hallo</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "terms.tbx")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(tbxContent))
	writer.Close()

	rec = testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import-tbx", baseID),
		&buf, writer.FormDataContentType(), user.ID, tenant.ID)

	// Concept import is multilingual: the es/de concept IS imported (stored), even
	// though it doesn't match the base's en→fr default view. So it's a 200, not a
	// language-mismatch rejection.
	testutil.AssertStatus(t, rec, http.StatusOK)
	var importResp struct {
		Imported int `json:"imported"`
	}
	testutil.DecodeJSON(t, rec, &importResp)
	if importResp.Imported != 1 {
		t.Errorf("expected the es/de concept to import; got %d", importResp.Imported)
	}

	// The imported es/de terms are stored and drive the base's derived
	// target_languages (source is en, so both es and de become targets).
	if err := q.SyncTermBaseTargetLanguages(context.Background(), baseID); err != nil {
		t.Fatal(err)
	}
	langs, err := q.ListTermBaseLanguages(context.Background(), baseID)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(langs, ",")
	if got != "de,es" {
		t.Errorf("expected languages de,es present after import; got %q", got)
	}
}

// Uploading a non-TBX (or DCT-style) file must be rejected with an actionable
// 400, not silently imported as 0 concepts.
func TestIntegrationTBXImportUnsupported(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Unsupported", "source_language": "en", "target_language": "fr",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)

	// Not a TBX file at all (Android strings.xml root).
	content := `<?xml version="1.0"?><resources><string name="x">y</string></resources>`
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "terms.xml")
	part.Write([]byte(content))
	writer.Close()

	rec = testutil.DoRequestRaw(t, router, "POST", fmt.Sprintf("/api/terminology/%d/import-tbx", baseResp.Data.ID),
		&buf, writer.FormDataContentType(), user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	var errResp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	testutil.DecodeJSON(t, rec, &errResp)
	if !bytes.Contains([]byte(errResp.Error.Message), []byte("TBX")) {
		t.Errorf("expected an actionable 'not a TBX' message, got %q", errResp.Error.Message)
	}
}

// A source term can map to different concepts across domains ("pin" electrical
// vs jewellery). When the project has a domain, the matching concept ranks first.
func TestIntegrationTermLookupDomainPreference(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	ctx := context.Background()

	base, err := q.CreateTermBase(ctx, db.CreateTermBaseParams{
		TenantID: tenant.ID, Name: "Homonyms", SourceLanguage: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same source "pin" in two concepts with different domains.
	for _, c := range []struct{ tgt, domain string }{{"clavija", "Electrical system"}, {"broche", "Jewellery"}} {
		ci := terminology.ConceptInput{Domain: c.domain, Terms: []terminology.ConceptTermInput{
			{Language: "en", Text: "pin", Status: "approved"},
			{Language: "es", Text: c.tgt, Status: "approved"},
		}}
		if _, err := terminology.CreateConcept(ctx, q, base, ci, nil); err != nil {
			t.Fatal(err)
		}
	}

	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Homonyms Template")
	if _, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: tmpl.SettingsProfileID, TermBaseID: base.ID,
		SourceLanguage: "en", TargetLanguage: "es", Mode: "read", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := q.LookupSettingsTermsByTokens(ctx, db.LookupSettingsTermsByTokensParams{
		SettingsProfileID: tmpl.SettingsProfileID,
		SourceLanguage:    "en",
		TargetLanguage:    "es",
		Tokens:            []string{"pin"},
		ProjectDomain:     "Electrical system",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 concept rows for 'pin', got %d", len(rows))
	}
	if rows[0].Domain != "Electrical system" {
		t.Errorf("domain-matching concept should rank first; got %q first", rows[0].Domain)
	}
}

func TestIntegrationTermLookup(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	ctx := context.Background()

	base, err := q.CreateTermBase(ctx, db.CreateTermBaseParams{
		TenantID: tenant.ID, Name: "Lookup Test", SourceLanguage: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	mustCreateEntry(t, q, base, "password", "contraseña", "es", "approved")
	mustCreateEntry(t, q, base, "email", "correo electrónico", "es", "approved")

	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Lookup Template")
	q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
		SettingsProfileID: tmpl.SettingsProfileID, TermBaseID: base.ID,
		SourceLanguage: "en", TargetLanguage: "es",
		Mode: "read", Priority: 1,
	})

	// Lookup terms — source text containing "password"
	url := fmt.Sprintf("/api/terminology/lookup?template_id=%d&source_language=en&target_language=es&source_text=Reset+your+password+now", tmpl.ID)
	rec := testutil.DoRequest(t, router, "GET", url, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var lookupResp struct {
		Data []struct {
			SourceTerm string `json:"source_term"`
			TargetTerm string `json:"target_term"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &lookupResp)

	if len(lookupResp.Data) != 1 {
		t.Fatalf("expected 1 matching term (password), got %d", len(lookupResp.Data))
	}
	if lookupResp.Data[0].SourceTerm != "password" {
		t.Errorf("expected 'password', got %q", lookupResp.Data[0].SourceTerm)
	}
}

func TestIntegrationTemplateTermAssignments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	ctx := context.Background()

	base, _ := q.CreateTermBase(ctx, db.CreateTermBaseParams{
		TenantID: tenant.ID, Name: "Test Term Base", SourceLanguage: "en",
	})
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Assign Test")

	rec := testutil.DoRequest(t, router, "PUT", fmt.Sprintf("/api/templates/%d/terminology", tmpl.ID), map[string]any{
		"assignments": []map[string]any{
			{"term_base_id": base.ID, "source_language": "en", "target_language": "es", "mode": "read", "priority": 1},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data []struct {
			TermBaseID     int64  `json:"term_base_id"`
			SourceLanguage string `json:"source_language"`
			Mode           string `json:"mode"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(resp.Data))
	}
	if resp.Data[0].TermBaseID != base.ID {
		t.Errorf("expected term_base_id %d, got %d", base.ID, resp.Data[0].TermBaseID)
	}

	// Verify template GET includes term_assignments
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/templates/%d", tmpl.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var getResp struct {
		Data struct {
			TermAssignments []struct {
				TermBaseID int64 `json:"term_base_id"`
			} `json:"term_assignments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &getResp)
	if len(getResp.Data.TermAssignments) != 1 {
		t.Fatalf("expected 1 term assignment in GET response, got %d", len(getResp.Data.TermAssignments))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// A term base's target_languages are derived from its terms (source excluded) and
// re-synced on term create/delete — not set at creation.
func TestIntegrationTermBaseTargetLanguagesSync(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Create a base with a source language only (no target).
	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Multi", "source_language": "en",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID              int64    `json:"id"`
			TargetLanguages []string `json:"target_languages"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID
	if len(baseResp.Data.TargetLanguages) != 0 {
		t.Errorf("new base should have no target_languages, got %v", baseResp.Data.TargetLanguages)
	}

	targetsNow := func() string {
		rec := testutil.DoRequest(t, router, "GET", "/api/terminology", nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var lr struct {
			Data []struct {
				ID              int64    `json:"id"`
				TargetLanguages []string `json:"target_languages"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &lr)
		for _, b := range lr.Data {
			if b.ID == baseID {
				return strings.Join(b.TargetLanguages, ",")
			}
		}
		return "<base missing>"
	}

	// Add a concept with en + de + fr terms → targets = {de, fr}.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
		"terms": []map[string]string{
			{"language": "en", "text": "car"},
			{"language": "de", "text": "Auto"},
			{"language": "fr", "text": "voiture"},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	if got := targetsNow(); got != "de,fr" {
		t.Errorf("after adding en/de/fr terms, target_languages = %q, want \"de,fr\"", got)
	}

	// Find and delete the fr term → targets drop to {de}.
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, created.Data.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got struct {
		Data struct {
			Terms []struct {
				ID       int64  `json:"id"`
				Language string `json:"language"`
			} `json:"terms"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &got)
	var frID int64
	for _, tm := range got.Data.Terms {
		if tm.Language == "fr" {
			frID = tm.ID
		}
	}
	if frID == 0 {
		t.Fatal("fr term not found")
	}
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms/%d", baseID, created.Data.ID, frID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if got := targetsNow(); got != "de" {
		t.Errorf("after deleting the fr term, target_languages = %q, want \"de\"", got)
	}
}

// A concept never holds two identical terms in a language: duplicates are dropped
// on create, and an inline add of an existing term is idempotent (returns it).
// Text match is exact (case-sensitive); language is normalized.
func TestIntegrationConceptTermDedup(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/terminology", map[string]string{
		"name": "Dedup", "source_language": "en",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var baseResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &baseResp)
	baseID := baseResp.Data.ID

	// Create with a duplicate en "car" + a de term → dedup leaves 2 terms.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts", baseID), map[string]any{
		"terms": []map[string]string{
			{"language": "en", "text": "car"},
			{"language": "en", "text": "car"},
			{"language": "de", "text": "Auto"},
		},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	conceptID := created.Data.ID

	termCount := func() int {
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/terminology/%d/concepts/%d", baseID, conceptID), nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var r struct {
			Data struct {
				Terms []struct{ ID int64 } `json:"terms"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &r)
		return len(r.Data.Terms)
	}
	if n := termCount(); n != 2 {
		t.Fatalf("create dedup: want 2 terms (en car + de Auto), got %d", n)
	}

	// Inline add of the existing en "car" → idempotent, still 2 terms.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms", baseID, conceptID), map[string]string{
		"language": "en", "text": "car",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	if n := termCount(); n != 2 {
		t.Errorf("add-term dedup: adding existing 'car' should be a no-op, got %d terms", n)
	}

	// A different case IS a distinct term (exact text match) → 3 terms.
	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/terminology/%d/concepts/%d/terms", baseID, conceptID), map[string]string{
		"language": "en", "text": "CAR",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	if n := termCount(); n != 3 {
		t.Errorf("add-term: 'CAR' differs from 'car' (exact match), want 3 terms, got %d", n)
	}
}
