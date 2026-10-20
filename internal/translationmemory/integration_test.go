package translationmemory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

func TestIntegrationTMCollectionCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// Create TM — source_language + target_languages are required,
	// customer is optional (omitted here).
	rec := testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{
		"name":             "Project TM",
		"source_language":  "en",
		"target_languages": []string{"es", "fr"},
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID              int64    `json:"id"`
			Name            string   `json:"name"`
			SourceLanguage  *string  `json:"source_language"`
			TargetLanguages []string `json:"target_languages"`
			CustomerID      *int64   `json:"customer_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	tmID := resp.Data.ID
	if resp.Data.Name != "Project TM" {
		t.Errorf("expected 'Project TM', got %q", resp.Data.Name)
	}
	if resp.Data.SourceLanguage == nil || *resp.Data.SourceLanguage != "en" {
		t.Errorf("expected source_language 'en', got %v", resp.Data.SourceLanguage)
	}
	if len(resp.Data.TargetLanguages) != 2 || resp.Data.TargetLanguages[0] != "es" || resp.Data.TargetLanguages[1] != "fr" {
		t.Errorf("expected target_languages [es fr], got %v", resp.Data.TargetLanguages)
	}
	if resp.Data.CustomerID != nil {
		t.Errorf("expected nil customer_id, got %v", *resp.Data.CustomerID)
	}

	// List TMs
	rec = testutil.DoRequest(t, router, "GET", "/api/tms", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 1 {
		t.Errorf("expected 1 TM, got %d", len(listResp.Data))
	}

	// Rename
	rec = testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/tms/%d", tmID), map[string]string{
		"name": "Renamed TM",
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var renamed struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &renamed)
	if renamed.Data.Name != "Renamed TM" {
		t.Errorf("PATCH response name = %q, want %q", renamed.Data.Name, "Renamed TM")
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/tms/%d", tmID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var reread struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &reread)
	if reread.Data.Name != "Renamed TM" {
		t.Errorf("GET after rename: name = %q, want %q", reread.Data.Name, "Renamed TM")
	}

	// Delete
	rec = testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/tms/%d", tmID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNoContent)

	// Soft delete: the row stays with deleted_at, reads skip it.
	var deleted bool
	if err := pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM tm_collections WHERE id = $1`, tmID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("TM row should remain with deleted_at set: deleted=%v err=%v", deleted, err)
	}
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/tms/%d", tmID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	rec = testutil.DoRequest(t, router, "GET", "/api/tms", nil, user.ID, tenant.ID)
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("deleted TM still listed: %d", len(listResp.Data))
	}
}

// TestIntegrationTMCollectionCreateValidation covers create-time rules:
// source_language required, >=1 target_language required, target != source,
// and optional customer must belong to the caller's tenant.
func TestIntegrationTMCollectionCreateValidation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")

	// A customer in this tenant, and one in a DIFFERENT tenant (cross-tenant guard).
	ownCustomer, err := q.CreateCustomer(context.Background(), db.CreateCustomerParams{TenantID: tenant.ID, Name: "Acme"})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	otherTenant := testutil.SeedTenant(t, q, "OtherCorp")
	foreignCustomer, err := q.CreateCustomer(context.Background(), db.CreateCustomerParams{TenantID: otherTenant.ID, Name: "Foreign"})
	if err != nil {
		t.Fatalf("seed foreign customer: %v", err)
	}

	badCases := []struct {
		name string
		body map[string]any
	}{
		{"missing source", map[string]any{"name": "TM", "target_languages": []string{"es"}}},
		{"empty targets", map[string]any{"name": "TM", "source_language": "en", "target_languages": []string{}}},
		{"target equals source", map[string]any{"name": "TM", "source_language": "en", "target_languages": []string{"en"}}},
		{"duplicate targets", map[string]any{"name": "TM", "source_language": "en", "target_languages": []string{"es", "es"}}},
		{"missing name", map[string]any{"source_language": "en", "target_languages": []string{"es"}}},
		{"foreign customer", map[string]any{"name": "TM", "source_language": "en", "target_languages": []string{"es"}, "customer_id": foreignCustomer.ID}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, router, "POST", "/api/tms", tc.body, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
		})
	}

	// Happy path with a valid same-tenant customer — persists + returns customer_id.
	rec := testutil.DoRequest(t, router, "POST", "/api/tms", map[string]any{
		"name":             "Client TM",
		"source_language":  "EN", // normalized to "en"
		"target_languages": []string{"ES", "pt-BR"},
		"customer_id":      ownCustomer.ID,
	}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			SourceLanguage  *string  `json:"source_language"`
			TargetLanguages []string `json:"target_languages"`
			CustomerID      *int64   `json:"customer_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.SourceLanguage == nil || *resp.Data.SourceLanguage != "en" {
		t.Errorf("expected normalized source 'en', got %v", resp.Data.SourceLanguage)
	}
	if len(resp.Data.TargetLanguages) != 2 || resp.Data.TargetLanguages[0] != "es" {
		t.Errorf("expected normalized targets [es pt-br], got %v", resp.Data.TargetLanguages)
	}
	if resp.Data.CustomerID == nil || *resp.Data.CustomerID != ownCustomer.ID {
		t.Errorf("expected customer_id %d, got %v", ownCustomer.ID, resp.Data.CustomerID)
	}
}

// TestIntegrationTMUpsertPipelineEntry covers the human-write dedupe path that
// pipeline TM write-back uses. The human write path keeps one row per source:
// the partial unique index is `(tm_id, source_lang,
// target_lang, source_hash) WHERE created_by = 'human'`, so the same source
// overwrites target_text in place, regardless of whether the target changed.
//
// TMX import keeps every target (`InsertTM`, created_by is the
// TMX creation tool); see TestIntegrationTMEntries for that side. The history snapshot of the prior target is wired in by
// pipeline.SaveSegmentToTM (not by this raw query), and is exercised in
// internal/pipeline/integration_test.go.
//
// This test exercises the SQL-level upsert behaviour directly. End-to-end
// coverage of the human path lives in j22 D3 + the pipeline integration tests.
func TestIntegrationTMUpsertPipelineEntry(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "PipelineCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "Pipeline TM")
	ctx := context.Background()

	// First call: insert "Hello"→"Bonjour".
	row1, err := q.UpsertTMPipelineEntry(ctx, db.UpsertTMPipelineEntryParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello", TargetText: "Bonjour",
		SourceRichText: "", TargetRichText: "",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if row1.UsageCount != 0 {
		t.Errorf("fresh insert: usage_count should start at 0, got %d", row1.UsageCount)
	}
	if row1.PrevEntryID != nil {
		t.Errorf("fresh insert: prev_entry_id should be NULL, got %d", *row1.PrevEntryID)
	}

	// Second call: SAME source + SAME target. Updates in place, usage_count++.
	row2, err := q.UpsertTMPipelineEntry(ctx, db.UpsertTMPipelineEntryParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello", TargetText: "Bonjour",
		SourceRichText: "", TargetRichText: "",
	})
	if err != nil {
		t.Fatalf("dedup upsert: %v", err)
	}
	if row2.ID != row1.ID {
		t.Errorf("dedup upsert must return SAME row id; got %d vs %d", row2.ID, row1.ID)
	}
	if row2.UsageCount != row1.UsageCount+1 {
		t.Errorf("dedup upsert must increment usage_count; got %d → %d", row1.UsageCount, row2.UsageCount)
	}
	if row2.PrevEntryID == nil || *row2.PrevEntryID != row1.ID {
		t.Errorf("re-confirm: prev_entry_id should = first row id (%d), got %v", row1.ID, row2.PrevEntryID)
	}
	if row2.PrevTargetText == nil || *row2.PrevTargetText != "Bonjour" {
		t.Errorf("prev_target_text should be the BEFORE value 'Bonjour', got %v", row2.PrevTargetText)
	}

	// Third call: SAME source + DIFFERENT target. Overwrites the
	// existing row in place; prev_target_text returns "Bonjour" so the
	// caller can archive it to translation_memory_history.
	row3, err := q.UpsertTMPipelineEntry(ctx, db.UpsertTMPipelineEntryParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello", TargetText: "Salut",
		SourceRichText: "", TargetRichText: "",
	})
	if err != nil {
		t.Fatalf("overwrite upsert: %v", err)
	}
	if row3.ID != row1.ID {
		t.Errorf("same source + different target overwrites in place, expected id %d got %d", row1.ID, row3.ID)
	}
	if row3.TargetText != "Salut" {
		t.Errorf("expected target_text overwritten to 'Salut', got %q", row3.TargetText)
	}
	if row3.PrevTargetText == nil || *row3.PrevTargetText != "Bonjour" {
		t.Errorf("prev_target_text on overwrite should be 'Bonjour', got %v", row3.PrevTargetText)
	}

	// List: exactly 1 row (the human path collapses to one entry per source).
	entries, err := q.ListTMEntries(ctx, db.ListTMEntriesParams{
		TmID: tm.ID, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListTMEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 row after 3 confirms (single-row dedupe); got %d", len(entries))
	}
}

// TestIntegrationTMEntries covers the basic InsertTM + LookupTM + ListTMEntries
// path used by TMX import. InsertTM is a PLAIN INSERT (no dedupe):
// TMX import must preserve multiple translations of the same source. That's why
// this test does NOT cover "insert same source twice → updates rather than
// duplicates" — that contract belongs to UpsertTMPipelineEntry (pipeline
// write-back path), which is exercised end-to-end in j22 phases D1-D3 and
// at the Go level in internal/pipeline/*_test.go.
func TestIntegrationTMEntries(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "TestCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "Test TM")

	ctx := context.Background()

	// Insert two distinct entries.
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello",
		TargetText:     "Bonjour",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	_, err = q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Goodbye",
		TargetText:     "Au revoir",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	// ListTMEntries: should see both rows.
	entries, err := q.ListTMEntries(ctx, db.ListTMEntriesParams{
		TmID:  tm.ID,
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListTMEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 TM entries, got %d", len(entries))
	}

	// LookupTM: exact match on "Hello".
	entry, err := q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTM: %v", err)
	}
	if entry.TargetText != "Bonjour" {
		t.Errorf("expected 'Bonjour', got %q", entry.TargetText)
	}

	// Inserting the SAME source again with a DIFFERENT target adds
	// a third row, NOT replacing the first. Frontend uses LookupTMAll to
	// surface all translations side-by-side; downstream dedup happens via
	// UpsertTMPipelineEntry on the human-write path only.
	_, err = q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello",
		TargetText:     "Salut", // different target for same source
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("InsertTM (multi-target): %v", err)
	}

	entries, err = q.ListTMEntries(ctx, db.ListTMEntriesParams{
		TmID:  tm.ID,
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListTMEntries (after multi-target): %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("same source + different target must add a third row "+
			"(InsertTM is a plain insert, not an upsert). Got %d entries.", len(entries))
	}
}

func TestIntegrationTMTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "Company A")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test.com", "pass")
	testutil.SeedTM(t, q, tenantA.ID, "A's TM")

	tenantB := testutil.SeedTenant(t, q, "Company B")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	// Tenant B should see zero TMs
	rec := testutil.DoRequest(t, router, "GET", "/api/tms", nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var listResp struct {
		Data []struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 0 {
		t.Errorf("tenant B should see 0 TMs, got %d", len(listResp.Data))
	}

	// Tenant A should see their TM
	rec = testutil.DoRequest(t, router, "GET", "/api/tms", nil, userA.ID, tenantA.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &listResp)
	if len(listResp.Data) != 1 {
		t.Errorf("tenant A should see 1 TM, got %d", len(listResp.Data))
	}
}

// TestIntegrationTM_RichTextRoundTrip verifies the R24 fix at the storage
// layer: an XLIFF tag-bearing pair stored via InsertTM (the TMX import path)
// preserves source_rich_text + target_rich_text on read-back, AND the plain
// source_text/target_text columns get the stripped form (used for matching
// + dedup hashing). Both halves matter — the rich form drives display, the
// plain form must be markup-free or fuzzy/exact match scoring breaks.
//
// Anti-tautology: passing "" rich → MUST store NULL (NULLIF contract). The
// second half of this test asserts that path so a regression that stored ""
// instead of NULL would be caught.
func TestIntegrationTM_RichTextRoundTrip(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "RichCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "Rich TM")

	srcRich := `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept>`
	tgtRich := `Haz clic <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept>`

	// Path 1: rich text provided — must persist exactly + show up on lookup.
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "Click here",
		TargetText:     "Haz clic aquí",
		CreatedBy:      "TMXImporter",
		TuID:           "",
		SourceRichText: srcRich,
		TargetRichText: tgtRich,
	}); err != nil {
		t.Fatalf("InsertTM with rich: %v", err)
	}

	rows, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Click here",
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("LookupTMAll: rows=%d err=%v", len(rows), err)
	}
	if rows[0].SourceRichText == nil {
		t.Fatalf("source_rich_text is NULL — InsertTM didn't write rich text. Check the InsertTM SQL's NULLIF args.")
	}
	if *rows[0].SourceRichText != srcRich {
		t.Errorf("source_rich_text round-trip failed:\n  want: %q\n  got:  %q", srcRich, *rows[0].SourceRichText)
	}
	if rows[0].TargetRichText == nil || *rows[0].TargetRichText != tgtRich {
		t.Errorf("target_rich_text round-trip failed")
	}
	// Plain text columns must be the stripped form so matching + dedup work.
	if rows[0].SourceText != "Click here" {
		t.Errorf("plain source_text mismatch: got %q (must be the markup-stripped form)", rows[0].SourceText)
	}

	// Path 2: empty rich text → NULL (NOT empty string), so the frontend
	// fallback path triggers correctly. This is the NULLIF contract — if a
	// regression stored "" instead, the frontend's `rich_text != null` check
	// would falsely trigger and try to render an empty chip block.
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "Plain segment",
		TargetText:     "Segmento llano",
		CreatedBy:      "TMXImporter",
		TuID:           "",
		SourceRichText: "",
		TargetRichText: "",
	}); err != nil {
		t.Fatalf("InsertTM without rich: %v", err)
	}
	plainRows, _ := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Plain segment",
	})
	if len(plainRows) != 1 {
		t.Fatalf("expected 1 plain row, got %d", len(plainRows))
	}
	if plainRows[0].SourceRichText != nil {
		t.Errorf("empty rich must store as NULL (NULLIF contract), got %q", *plainRows[0].SourceRichText)
	}
	if plainRows[0].TargetRichText != nil {
		t.Errorf("empty rich must store as NULL (NULLIF contract), got %q", *plainRows[0].TargetRichText)
	}
}

// ─── HandleUpdateEntry — TM inline edit. Each test exercises ONE invariant of
//      the PATCH /api/tms/{tmID}/entries/{entryID} surface end-to-end (HTTP
//      → handler → tx → DB → response). All five together pin: happy path,
//      tag-bearing edit, tenant isolation, optimistic locking 409, and the
//      legacy no-version-check path.

// TestIntegrationTMUpdateEntry_HappyPath edits a plain TM entry and asserts
// the entry's plain text + updated_at change AND a single history row was
// appended capturing the BEFORE values. This is the basic round-trip — if
// it doesn't pass, nothing else will.
func TestIntegrationTMUpdateEntry_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EditCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "editor@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Editable TM")

	// Seed an entry directly via the query (faster than going through
	// TMX import — that path is already covered).
	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Original source", TargetText: "Original target",
		CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	beforeUpdatedAt := entry.UpdatedAt.Time

	// PATCH with new values + change_reason. No expected_updated_at — exercise
	// the legacy path here; the 409 path gets its own dedicated test.
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":   "Edited source",
			"target_text":   "Edited target",
			"change_reason": "edit",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// 1. The entry's plain text should now be the new values, and
	//    updated_at should have advanced.
	after, err := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("re-fetch entry: %v", err)
	}
	if after.SourceText != "Edited source" {
		t.Errorf("source_text not updated: got %q", after.SourceText)
	}
	if after.TargetText != "Edited target" {
		t.Errorf("target_text not updated: got %q", after.TargetText)
	}
	if !after.UpdatedAt.Time.After(beforeUpdatedAt) {
		t.Errorf("updated_at didn't advance — was %v, now %v", beforeUpdatedAt, after.UpdatedAt.Time)
	}
	if after.UpdatedBy != "editor@test.com" {
		t.Errorf("updated_by must be the editor's email, got %q", after.UpdatedBy)
	}

	// 2. ONE history row should exist, capturing the BEFORE values.
	history, err := q.ListTMEntryHistory(ctx, entry.ID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected exactly 1 history row, got %d", len(history))
	}
	h := history[0]
	if h.SourceText != "Original source" {
		t.Errorf("history.source_text must be the BEFORE value, got %q", h.SourceText)
	}
	if h.TargetText != "Original target" {
		t.Errorf("history.target_text must be the BEFORE value, got %q", h.TargetText)
	}
	if h.ChangedByUserID == nil || *h.ChangedByUserID != user.ID {
		t.Errorf("history.changed_by_user_id must be the editing user, got %v", h.ChangedByUserID)
	}
	if h.ChangeReason != "edit" {
		t.Errorf("history.change_reason: got %q", h.ChangeReason)
	}
	if h.ChangedByEmail == nil || *h.ChangedByEmail != "editor@test.com" {
		t.Errorf("history join to users for display: got %v", h.ChangedByEmail)
	}
}

// TestIntegrationTMUpdateEntry_TagBearing verifies that editing a tag-
// bearing entry preserves the OLD rich_text in history AND saves the NEW
// rich_text on the entry. This is what makes "view history → restore old
// chips" work in the TM entry history dialog.
func TestIntegrationTMUpdateEntry_TagBearing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TagEditCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tag-editor@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Tag TM")

	beforeRich := `Click <bpt id="1" displaytext="[B]"/>here<ept id="1" displaytext="[/B]"/>`
	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Click here", TargetText: "Haz clic aquí",
		SourceRichText: beforeRich, TargetRichText: "",
		CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	afterRich := `Click <bpt id="1" displaytext="[STRONG]"/>here<ept id="1" displaytext="[/STRONG]"/>`
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":      "Click here",
			"target_text":      "Haz clic aquí",
			"source_rich_text": afterRich,
			"target_rich_text": "",
			"change_reason":    "edit",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Entry's rich_text should now be the NEW value.
	after, _ := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if after.SourceRichText == nil || *after.SourceRichText != afterRich {
		t.Errorf("source_rich_text not updated to new value")
	}

	// History should preserve the OLD rich_text — this is the recovery path
	// the history dialog restores from.
	history, _ := q.ListTMEntryHistory(ctx, entry.ID)
	if len(history) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(history))
	}
	if history[0].SourceRichText == nil || *history[0].SourceRichText != beforeRich {
		t.Errorf("history.source_rich_text must preserve the BEFORE rich form, got %v",
			history[0].SourceRichText)
	}
	if history[0].TargetRichText != nil {
		t.Errorf("history.target_rich_text must mirror the BEFORE NULL state, got %q",
			*history[0].TargetRichText)
	}
}

// TestIntegrationTMUpdateEntry_TenantIsolation verifies that a user from
// tenant A can't PATCH a TM entry that belongs to tenant B's TM, even by
// passing tenant B's tmID + entryID in the URL. The middleware blocks the
// route at tmID resolution — should return 404 (not 403, to avoid leaking
// existence). Mirrors the pattern asserted in TestIntegrationTMTenantIsolation
// for the read path.
func TestIntegrationTMUpdateEntry_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// Tenant A — owns the TM + entry.
	tenantA := testutil.SeedTenant(t, q, "Tenant A")
	tmA := testutil.SeedTM(t, q, tenantA.ID, "A's TM")
	entryA, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmA.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "A's source", TargetText: "A's target", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry A: %v", err)
	}

	// Tenant B — the attacker.
	tenantB := testutil.SeedTenant(t, q, "Tenant B")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	// User B tries to PATCH Tenant A's entry through Tenant A's tmID URL.
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tmA.ID, entryA.ID),
		map[string]any{
			"source_text": "PWNED",
			"target_text": "PWNED",
		}, userB.ID, tenantB.ID)
	if rec.Code == http.StatusOK {
		t.Fatalf("CRITICAL: cross-tenant PATCH succeeded (got 200). Expected 404.")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 (no-existence-leak); got %d", rec.Code)
	}

	// Defensive: A's entry must be untouched.
	after, _ := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entryA.ID, TenantID: tenantA.ID,
	})
	if after.SourceText != "A's source" {
		t.Errorf("CRITICAL: A's entry was modified by B's PATCH attempt; source=%q", after.SourceText)
	}
	// And no history row should exist (tx rolled back).
	history, _ := q.ListTMEntryHistory(ctx, entryA.ID)
	if len(history) != 0 {
		t.Errorf("history row leaked from rejected cross-tenant PATCH: %d rows", len(history))
	}
}

// TestIntegrationTMUpdateEntry_StaleVersion409 is the optimistic-locking
// invariant (mirroring translations). Two admins editing the same row: the second saver gets
// a 409, NOT a silent overwrite. The first admin's changes survive.
//
// Anti-tautology: assertions check BOTH the 409 status AND that the row's
// plain text is the FIRST writer's value. If the implementation accidentally
// returned 409 but ALSO committed the second write, the second assertion
// fails — distinguishing "rejected with side-effect" from "rejected cleanly."
func TestIntegrationTMUpdateEntry_StaleVersion409(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ConflictCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Conflict TM")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Original", TargetText: "Original", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	staleStamp := entry.UpdatedAt.Time

	// First write — uses the original baseline, succeeds, advances updated_at.
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":         "First writer wins",
			"target_text":         "First writer target",
			"expected_updated_at": staleStamp.Format(time.RFC3339Nano),
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Second write — sends the SAME (now stale) baseline. Must 409.
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":         "Second writer (should be rejected)",
			"target_text":         "Second writer (should be rejected)",
			"expected_updated_at": staleStamp.Format(time.RFC3339Nano),
		}, user.ID, tenant.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale baseline must 409; got %d body=%s",
			rec.Code, rec.Body.String())
	}

	// Defensive (anti-tautology): the row must hold the FIRST writer's value.
	// If the second write somehow landed despite the 409, this catches it.
	after, _ := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if after.SourceText != "First writer wins" {
		t.Errorf("rejected second write LANDED — row source_text=%q, expected first-writer value",
			after.SourceText)
	}

	// History should have ONE row (from the first write only — the rejected
	// second write rolled its tx back, including the history insert).
	history, _ := q.ListTMEntryHistory(ctx, entry.ID)
	if len(history) != 1 {
		t.Errorf("history should have 1 row (only the successful first write); got %d. "+
			"More means the rejected write's history insert wasn't rolled back — broken atomicity.",
			len(history))
	}
}

// TestIntegrationTMUpdateEntry_NoVersionCheckLegacy verifies the
// "expected_updated_at omitted → no version check" contract. This is the
// same legacy escape hatch translations keep: scripted
// callers / background workers that don't track updated_at can still write.
// The PATCH succeeds even when the row was edited mid-flight.
func TestIntegrationTMUpdateEntry_NoVersionCheckLegacy(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "LegacyCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "u@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Legacy TM")

	entry, _ := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Original", TargetText: "Original", CreatedBy: "test", TuID: "",
	})

	// First write — bumps updated_at.
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text": "First write",
			"target_text": "First target",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Second write WITHOUT expected_updated_at — must succeed despite the
	// row having advanced. This is the legacy contract that scripted callers
	// rely on. If it 409s, we broke backward compat with the translations pattern.
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text": "Second write wins (no version check)",
			"target_text": "Second target",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	after, _ := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if after.SourceText != "Second write wins (no version check)" {
		t.Errorf("legacy path didn't apply: source_text=%q", after.SourceText)
	}
}

// TestIntegrationTMDeleteEntry_CrossTenantAttack pins that a cross-tenant
// `DELETE /api/tms/{my_tm}/entries/{their_entry}` is 404 and deletes nothing:
// DeleteTMEntry is scoped by `tm_id = $2`.
//
// This is the security half of the j25 gauntlet, encoded as a Go
// integration test for fast regression coverage on every `make test` run.
func TestIntegrationTMDeleteEntry_CrossTenantAttack(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// Tenant A — owns the target entry.
	tenantA := testutil.SeedTenant(t, q, "Tenant A")
	tmA := testutil.SeedTM(t, q, tenantA.ID, "A's TM")
	victim, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmA.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Victim entry", TargetText: "Víctima", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed victim entry: %v", err)
	}

	// Tenant B — the attacker (admin role to clear RequireRole).
	tenantB := testutil.SeedTenant(t, q, "Tenant B")
	attacker := testutil.SeedUser(t, q, tenantB.ID, "attacker@test.com", "pass")
	tmB := testutil.SeedTM(t, q, tenantB.ID, "Attacker's TM")

	// Attack: hit Tenant B's TM URL with Tenant A's entry ID.
	rec := testutil.DoRequest(t, router, "DELETE",
		fmt.Sprintf("/api/tms/%d/entries/%d", tmB.ID, victim.ID),
		nil, attacker.ID, tenantB.ID)

	// CORE INVARIANT: must 404 (no info leak), must NOT delete the row.
	if rec.Code == http.StatusNoContent {
		t.Fatalf("CRITICAL: cross-tenant DELETE returned 204 — " +
			"victim's TM entry was destroyed. DeleteTMEntry SQL needs tenant scoping.")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 (no-existence-leak); got %d body=%s",
			rec.Code, rec.Body.String())
	}

	// Defensive: the victim row must still exist.
	survivor, err := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: victim.ID, TenantID: tenantA.ID,
	})
	if err != nil {
		t.Fatalf("CRITICAL: victim entry was deleted by cross-tenant attack: %v", err)
	}
	if survivor.SourceText != "Victim entry" {
		t.Errorf("victim entry corrupted: source_text=%q", survivor.SourceText)
	}
}

// TestIntegrationTMDeleteEntry_CrossTMSameTenant pins the DELETE
// surface of cross-TM URL scoping: deleting an entry through a DIFFERENT TM's URL in the same tenant
// (URL tampering) is 404 when entry.tm_id doesn't match URL.tmID.
func TestIntegrationTMDeleteEntry_CrossTMSameTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "Tenant")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	tm1 := testutil.SeedTM(t, q, tenant.ID, "TM 1")
	tm2 := testutil.SeedTM(t, q, tenant.ID, "TM 2")

	// Entry lives in TM 2.
	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm2.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Lives in TM 2", TargetText: "Vive en TM 2", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	// Address it via TM 1's URL — both TMs are owned by the user, so
	// RequireTMOwnership + RequireRole both pass. The handler must still 404.
	rec := testutil.DoRequest(t, router, "DELETE",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm1.ID, entry.ID),
		nil, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-TM same-tenant DELETE must 404 (entry doesn't live in URL's TM); got %d", rec.Code)
	}

	// Entry must survive.
	survivor, err := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("entry was destroyed by cross-TM URL tamper: %v", err)
	}
	if survivor.TmID != tm2.ID {
		t.Errorf("survivor.TmID = %d, expected %d (TM 2)", survivor.TmID, tm2.ID)
	}
}

// TestIntegrationTMUpdateEntry_CrossTMSameTenant: PATCH variant of the same
// check: addressing an entry via the wrong TM's URL is 404, so the
// audit trail never says "edited via TM A" for an entry that lives in TM B.
func TestIntegrationTMUpdateEntry_CrossTMSameTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "Tenant")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	tm1 := testutil.SeedTM(t, q, tenant.ID, "TM 1")
	tm2 := testutil.SeedTM(t, q, tenant.ID, "TM 2")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm2.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Lives in TM 2", TargetText: "Vive en TM 2", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm1.ID, entry.ID),
		map[string]any{
			"source_text": "Lives in TM 2",
			"target_text": "TAMPERED via wrong TM URL",
		}, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-TM same-tenant PATCH must 404; got %d", rec.Code)
	}

	// Verify the entry's target_text was NOT modified.
	after, _ := q.GetTMEntryTenant(ctx, db.GetTMEntryTenantParams{
		ID: entry.ID, TenantID: tenant.ID,
	})
	if after.TargetText != "Vive en TM 2" {
		t.Errorf("entry was modified by cross-TM PATCH; target_text=%q", after.TargetText)
	}
	// And no history row from the rejected PATCH (the GetTMEntryScoped pre-check
	// rejects before the tx even begins).
	history, _ := q.ListTMEntryHistory(ctx, entry.ID)
	if len(history) != 0 {
		t.Errorf("rejected cross-TM PATCH leaked %d history rows", len(history))
	}
}

// ─── HandleTMFindReplace — bulk find & replace across TM entries.
//      Tests cover: find-mode preview, replace_all
//      with per-entry history, case sensitivity, scope (source/target/both),
//      validation, and transactional atomicity (a partial failure must roll
//      back the entire batch — not "5 of 10 entries written.")

// TestIntegrationTMFindReplace_FindModeOnly verifies that "find" mode is
// pure preview: returns matches but never mutates the DB. Anti-tautology
// guard: re-fetch entries after the call and assert each is byte-for-byte
// identical to the seeded value.
func TestIntegrationTMFindReplace_FindModeOnly(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FRCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "fr@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "FR TM")

	for _, e := range []struct{ src, tgt string }{
		{"Hello world", "Hola mundo"},
		{"Hello there", "Hola allí"},
		{"Goodbye now", "Adiós ahora"},
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test", TuID: "",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find":    "Hello",
			"replace": "Hi",
			"mode":    "find",
			"scope":   "source",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var env struct {
		Data struct {
			TotalEntries      int `json:"total_entries"`
			TotalReplacements int `json:"total_replacements"`
			Matches           []struct {
				EntryID          int64  `json:"entry_id"`
				NewSourceText    string `json:"new_source_text"`
				SourceMatchCount int    `json:"source_match_count"`
				TargetMatchCount int    `json:"target_match_count"`
			} `json:"matches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, rec.Body.String())
	}
	if env.Data.TotalEntries != 2 {
		t.Errorf("expected 2 matching entries, got %d", env.Data.TotalEntries)
	}
	if env.Data.TotalReplacements != 2 {
		t.Errorf("expected 2 total replacements, got %d", env.Data.TotalReplacements)
	}
	for _, m := range env.Data.Matches {
		if m.SourceMatchCount != 1 {
			t.Errorf("each Hello-row should report exactly 1 source match, got %d", m.SourceMatchCount)
		}
		if m.TargetMatchCount != 0 {
			t.Errorf("scope=source must not match in target, got %d target matches", m.TargetMatchCount)
		}
		if !strings.HasPrefix(m.NewSourceText, "Hi") {
			t.Errorf("computed new_source_text should start with 'Hi', got %q", m.NewSourceText)
		}
	}

	// Anti-tautology: nothing was written. Re-fetch and assert.
	entries := tmEntries(t, q, tm.ID)
	helloPrefixes := 0
	for _, e := range entries {
		if strings.HasPrefix(e.SourceText, "Hello") {
			helloPrefixes++
		}
	}
	if helloPrefixes != 2 {
		t.Errorf("CRITICAL: find mode mutated rows — expected 2 Hello-prefixed entries unchanged, got %d", helloPrefixes)
	}
	// And no history rows should exist (preview mode never writes).
	for _, e := range entries {
		hist, _ := q.ListTMEntryHistory(ctx, e.ID)
		if len(hist) != 0 {
			t.Errorf("CRITICAL: find mode wrote history rows — entry %d has %d", e.ID, len(hist))
		}
	}
}

// TestIntegrationTMFindReplace_ReplaceAllAppliesAndAuditsEverything verifies
// the apply path: every matching entry is updated, each gets exactly one
// history row capturing BEFORE values, the response carries accurate counts.
func TestIntegrationTMFindReplace_ReplaceAllAppliesAndAuditsEverything(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FRCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "fr@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "FR TM")

	for _, e := range []struct{ src, tgt string }{
		{"contraseña reset", "password reset"},
		{"contraseña change", "password change"},
		{"unrelated entry", "no match here"},
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "es", TargetLanguage: "en",
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test", TuID: "",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find":    "contraseña",
			"replace": "clave",
			"mode":    "replace_all",
			"scope":   "source",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var env struct {
		Data struct {
			EntriesUpdated    int `json:"entries_updated"`
			TotalReplacements int `json:"total_replacements"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.EntriesUpdated != 2 {
		t.Errorf("expected entries_updated=2, got %d", env.Data.EntriesUpdated)
	}
	if env.Data.TotalReplacements != 2 {
		t.Errorf("expected total_replacements=2, got %d", env.Data.TotalReplacements)
	}

	// 1. Affected entries actually have the new source.
	entries := tmEntries(t, q, tm.ID)
	updated := 0
	untouched := 0
	for _, e := range entries {
		if strings.HasPrefix(e.SourceText, "clave") {
			updated++
			if e.UpdatedBy != "fr@test.com" {
				t.Errorf("entry %d: updated_by must be the editing user, got %q", e.ID, e.UpdatedBy)
			}
		}
		if e.SourceText == "unrelated entry" {
			untouched++
		}
	}
	if updated != 2 {
		t.Errorf("expected 2 entries updated to start with 'clave', got %d", updated)
	}
	if untouched != 1 {
		t.Errorf("expected 1 entry to remain untouched, got %d", untouched)
	}

	// 2. Each affected entry has exactly 1 history row with BEFORE values
	//    + change_reason="find_replace". Untouched entry has 0 history rows.
	for _, e := range entries {
		hist, _ := q.ListTMEntryHistory(ctx, e.ID)
		if strings.HasPrefix(e.SourceText, "clave") {
			if len(hist) != 1 {
				t.Errorf("entry %d (updated): expected 1 history row, got %d", e.ID, len(hist))
				continue
			}
			h := hist[0]
			if !strings.HasPrefix(h.SourceText, "contraseña") {
				t.Errorf("entry %d: history.source_text must hold the BEFORE value (contraseña...), got %q", e.ID, h.SourceText)
			}
			if h.ChangeReason != "find_replace" {
				t.Errorf("entry %d: change_reason should be 'find_replace', got %q", e.ID, h.ChangeReason)
			}
			if h.ChangedByUserID == nil || *h.ChangedByUserID != user.ID {
				t.Errorf("entry %d: changed_by_user_id mismatch, got %v", e.ID, h.ChangedByUserID)
			}
		} else {
			if len(hist) != 0 {
				t.Errorf("entry %d (untouched): expected 0 history rows, got %d", e.ID, len(hist))
			}
		}
	}
}

// TestIntegrationTMFindReplace_CaseSensitivity verifies the case_sensitive
// toggle via two adversarial cases:
//   - Mixed-case search "Hello": case-sensitive matches only "Hello" rows;
//     case-insensitive ALSO matches "hello", "HELLO", "hElLo".
//   - Replacement preserves verbatim casing — "Hello" stays "Hello" replacement
//     in both modes (no smart casing).
func TestIntegrationTMFindReplace_CaseSensitivity(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Case")
	user := testutil.SeedUser(t, q, tenant.ID, "case@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Case TM")

	for _, src := range []string{"Hello world", "hello world", "HELLO world", "hElLo there"} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: src, TargetText: "irrelevant", CreatedBy: "test", TuID: "",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Case-SENSITIVE search "Hello" → only the first row matches.
	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find": "Hello", "replace": "Bye", "mode": "find",
			"scope": "source", "case_sensitive": true,
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var caseSens struct {
		Data struct {
			TotalEntries int `json:"total_entries"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &caseSens)
	if caseSens.Data.TotalEntries != 1 {
		t.Errorf("case_sensitive=true should match exactly 1 'Hello' row, got %d", caseSens.Data.TotalEntries)
	}

	// Case-INSENSITIVE → all 4 rows match.
	rec = testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find": "Hello", "replace": "Bye", "mode": "find",
			"scope": "source", "case_sensitive": false,
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var caseInsens struct {
		Data struct {
			TotalEntries int `json:"total_entries"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &caseInsens)
	if caseInsens.Data.TotalEntries != 4 {
		t.Errorf("case_sensitive=false should match all 4 hello-variants, got %d", caseInsens.Data.TotalEntries)
	}
}

// TestIntegrationTMFindReplace_ScopeSourceVsTargetVsBoth pins the scope
// behavior. Same find term across all three modes.
func TestIntegrationTMFindReplace_ScopeSourceVsTargetVsBoth(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Scope")
	user := testutil.SeedUser(t, q, tenant.ID, "scope@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Scope TM")

	// Carefully chosen: "ABC" appears in source-only / target-only / both.
	rows := []struct{ src, tgt string }{
		{"ABC source", "no marker"},  // source-only
		{"no marker", "ABC target"},  // target-only
		{"ABC source", "ABC target"}, // both
		{"clean", "clean"},           // neither
	}
	for _, e := range rows {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test", TuID: "",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	cases := []struct {
		scope    string
		expected int
	}{
		{"source", 2}, // rows 1+3
		{"target", 2}, // rows 2+3
		{"both", 3},   // rows 1+2+3
	}
	for _, c := range cases {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
			map[string]any{
				"find": "ABC", "replace": "XYZ", "mode": "find", "scope": c.scope,
			}, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var env struct {
			Data struct {
				TotalEntries int `json:"total_entries"`
			} `json:"data"`
		}
		json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Data.TotalEntries != c.expected {
			t.Errorf("scope=%q: expected %d matching entries, got %d", c.scope, c.expected, env.Data.TotalEntries)
		}
	}
}

// TestIntegrationTMFindReplace_RichTextHandling pins the design choice on
// rich_text in bulk F&R: a matched field has its rich_text dropped to NULL
// (the tag positions are stale relative to the new plain text), an unmatched
// field preserves its rich_text verbatim.
//
// Why a test: the handler's branching logic on `wantSource`/`wantTarget` and
// `srcCount`/`tgtCount` is easy to break silently. Without this test, a
// future refactor could start preserving stale rich_text on matched rows
// (chips would render at the wrong positions) and nothing would fail.
func TestIntegrationTMFindReplace_RichTextHandling(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "RichCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "rich@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "Rich TM")

	// Two entries, both with rich_text on source AND target.
	// We'll do a target-scope F&R that only matches in target.
	const srcRich = "Press <ph id=\"1\" x=\"ENTER\"/> to submit"
	const tgtRich = "Stiskněte <ph id=\"1\" x=\"ENTER\"/> pro odeslání"
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "cs",
		SourceText:     "Press  to submit",
		TargetText:     "Stiskněte  pro odeslání",
		SourceRichText: srcRich,
		TargetRichText: tgtRich,
		CreatedBy:      "test", TuID: "",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Second entry: target has NO match for "odeslání", so this entry shouldn't update.
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "cs",
		SourceText:     "Press  to submit",
		TargetText:     "no marker here",
		SourceRichText: srcRich,
		TargetRichText: "",
		CreatedBy:      "test", TuID: "",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// F&R: replace "odeslání" in target only. Entry 1 matches, entry 2 doesn't.
	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find": "odeslání", "replace": "potvrzení",
			"mode": "replace_all", "scope": "target",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	entries := tmEntries(t, q, tm.ID)
	matchedIdx, untouchedIdx, nMatched, nUntouched := -1, -1, 0, 0
	for i, e := range entries {
		if strings.Contains(e.TargetText, "potvrzení") {
			matchedIdx, nMatched = i, nMatched+1
		} else if e.TargetText == "no marker here" {
			untouchedIdx, nUntouched = i, nUntouched+1
		}
	}
	if nMatched != 1 || nUntouched != 1 {
		t.Fatalf("want exactly one replaced entry and one untouched entry, got %d replaced, %d untouched (of %d)",
			nMatched, nUntouched, len(entries))
	}

	// MATCHED row: target rich_text must be DROPPED (NULL), source
	// rich_text preserved (source wasn't searched OR matched).
	matched := entries[matchedIdx]
	if matched.TargetText != "Stiskněte  pro potvrzení" {
		t.Errorf("entry %d (target matched): target_text = %q, want %q",
			matched.ID, matched.TargetText, "Stiskněte  pro potvrzení")
	}
	if matched.TargetRichText != nil {
		t.Errorf("entry %d (target matched): target_rich_text must be NULL after bulk replace, got %q",
			matched.ID, *matched.TargetRichText)
	}
	if matched.SourceRichText == nil || *matched.SourceRichText != srcRich {
		t.Errorf("entry %d (target matched, source untouched): source_rich_text must be preserved, got %v",
			matched.ID, matched.SourceRichText)
	}

	// UNTOUCHED row: its source rich_text must be preserved exactly.
	untouched := entries[untouchedIdx]
	if untouched.SourceRichText == nil || *untouched.SourceRichText != srcRich {
		t.Errorf("entry %d (untouched): source_rich_text must be preserved, got %v",
			untouched.ID, untouched.SourceRichText)
	}
}

// TestIntegrationTMFindReplace_BulkBypassesOptimisticLock pins the explicit
// design decision: bulk F&R does NOT enforce per-row optimistic locking.
// If a concurrent inline edit advances the row's updated_at while the bulk
// is in flight, the bulk wins (last-writer-wins). Per-row inline edit's
// optimistic lock is unaffected — only bulk bypasses it.
//
// This guards against a future refactor that adds expected_updated_at to
// the bulk path, which would cause partial bulk-applies in the wild.
func TestIntegrationTMFindReplace_BulkBypassesOptimisticLock(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "OL")
	user := testutil.SeedUser(t, q, tenant.ID, "ol@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "OL TM")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "find me", TargetText: "encuéntrame", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Step 1: someone (simulating a concurrent inline-editor) bumps updated_at
	// via a direct UpdateTMEntry call, BEFORE the bulk runs.
	if _, err := q.UpdateTMEntry(ctx, db.UpdateTMEntryParams{
		ID: entry.ID, TmID: tm.ID,
		SourceText: "find me", TargetText: "concurrent edit",
		SourceRichText: "", TargetRichText: "",
		UpdatedBy:         "concurrent",
		ExpectedUpdatedAt: pgtype.Timestamptz{Valid: false},
	}); err != nil {
		t.Fatalf("simulated concurrent edit: %v", err)
	}

	// Step 2: bulk F&R runs. Even though the row's updated_at advanced, bulk
	// has no baseline to compare — it applies without a version check:
	// 200 OK, source takes the bulk result ("replaced") and target keeps the
	// concurrent edit (scope=source never touches target).
	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{
			"find": "find me", "replace": "replaced",
			"mode": "replace_all", "scope": "source",
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Bulk succeeded — verify the row's final state is the union of both:
	// source from bulk, target from concurrent inline edit (untouched by
	// scope=source bulk).
	entries := tmEntries(t, q, tm.ID)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SourceText != "replaced" {
		t.Errorf("bulk should have applied: source_text=%q, expected 'replaced'", entries[0].SourceText)
	}
	if entries[0].TargetText != "concurrent edit" {
		t.Errorf("scope=source must NOT touch target: target_text=%q, expected the inline editor's value",
			entries[0].TargetText)
	}
}

// TestIntegrationTMFindReplace_TenantIsolation: tenant B cannot F&R tenant
// A's TM. Mirrors the j25 phase C cross-tenant guards for the new endpoint.
func TestIntegrationTMFindReplace_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "Tenant A")
	tmA := testutil.SeedTM(t, q, tenantA.ID, "A's TM")
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmA.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "secret", TargetText: "secreto", CreatedBy: "test", TuID: "",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tenantB := testutil.SeedTenant(t, q, "Tenant B")
	userB := testutil.SeedUser(t, q, tenantB.ID, "b@test.com", "pass")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/tms/%d/find-replace", tmA.ID),
		map[string]any{
			"find": "secret", "replace": "ATTACK", "mode": "replace_all",
		}, userB.ID, tenantB.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant F&R must 404 (no info leak), got %d", rec.Code)
	}

	// Defensive: the entry MUST be unchanged.
	entries := tmEntries(t, q, tmA.ID)
	if len(entries) == 0 || entries[0].SourceText != "secret" {
		t.Errorf("CRITICAL: cross-tenant F&R modified the row — source_text=%q", entries[0].SourceText)
	}
}

// TestIntegrationTMFindReplace_Validation pins the input-validation surface.
func TestIntegrationTMFindReplace_Validation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "V")
	user := testutil.SeedUser(t, q, tenant.ID, "v@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "V TM")

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty find", map[string]any{"find": "", "replace": "x"}, http.StatusBadRequest},
		{"missing find", map[string]any{"replace": "x"}, http.StatusBadRequest},
		{"bad mode", map[string]any{"find": "a", "mode": "DELETE_EVERYTHING"}, http.StatusBadRequest},
		{"bad scope", map[string]any{"find": "a", "scope": "everywhere"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, router, "POST",
				fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
				c.body, user.ID, tenant.ID)
			if rec.Code != c.want {
				t.Errorf("expected %d, got %d body=%s", c.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestIntegrationTMListEntryHistory_HappyPath exercises GET /api/tms/{tmID}/entries/{entryID}/history
// end-to-end: HTTP → handler → query → JSON response, covering the HTTP
// envelope, the auth/tenant gate, and the JSON marshaling that a
// queries-layer call (`q.ListTMEntryHistory(...)`) bypasses. The history-dialog test
// stack (TMEntryHistoryDialog.test.tsx) mocks the network — it can't catch
// shape drift between this handler and the frontend's `TMEntryHistory`
// type. This test pins the wire format for both.
func TestIntegrationTMListEntryHistory_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "HistoryCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "editor@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "History TM")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "v0 source", TargetText: "v0 target",
		CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	// 0 history rows initially → empty array, NOT null. (The frontend's
	// `useQuery` defaults `data` to undefined; an empty array is what makes
	// the "No past edits" empty state render correctly.)
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/tms/%d/entries/%d/history", tm.ID, entry.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var emptyEnv struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyEnv); err != nil {
		t.Fatalf("decode empty body: %v\nraw=%s", err, rec.Body.String())
	}
	if emptyEnv.Data == nil {
		t.Errorf("empty history must serialize as [] not null — frontend's empty-state branch depends on Array.isArray + length === 0")
	}
	if len(emptyEnv.Data) != 0 {
		t.Errorf("expected 0 history rows for fresh entry, got %d", len(emptyEnv.Data))
	}

	// Make 2 sequential edits to grow the history table.
	for _, target := range []string{"v1 target", "v2 target"} {
		patch := testutil.DoRequest(t, router, "PATCH",
			fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
			map[string]any{
				"source_text":   "v0 source",
				"target_text":   target,
				"change_reason": "edit",
			}, user.ID, tenant.ID)
		testutil.AssertStatus(t, patch, http.StatusOK)
	}

	// GET history → 2 rows, newest-first, each carrying the BEFORE values.
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/tms/%d/entries/%d/history", tm.ID, entry.ID),
		nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var env struct {
		Data []struct {
			ID             int64   `json:"id"`
			EntryID        int64   `json:"entry_id"`
			SourceText     string  `json:"source_text"`
			TargetText     string  `json:"target_text"`
			ChangeReason   string  `json:"change_reason"`
			CreatedAt      string  `json:"created_at"`
			ChangedByEmail *string `json:"changed_by_email"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode populated body: %v\nraw=%s", err, rec.Body.String())
	}
	if len(env.Data) != 2 {
		t.Fatalf("expected 2 history rows after 2 edits, got %d", len(env.Data))
	}
	// Newest-first: row[0] captured the state right before the v2 write
	// (which means it stores the v1 values), row[1] captures v0.
	if env.Data[0].TargetText != "v1 target" {
		t.Errorf("history[0].target_text (newest) must be the v1 BEFORE-snapshot, got %q", env.Data[0].TargetText)
	}
	if env.Data[1].TargetText != "v0 target" {
		t.Errorf("history[1].target_text (oldest) must be the v0 BEFORE-snapshot, got %q", env.Data[1].TargetText)
	}
	// changed_by_email is what the dialog renders as the author label.
	for i, h := range env.Data {
		if h.ChangedByEmail == nil {
			t.Errorf("history[%d].changed_by_email is null — frontend would show 'unknown user'; the LEFT JOIN to users isn't returning the editor", i)
			continue
		}
		if *h.ChangedByEmail != "editor@test.com" {
			t.Errorf("history[%d].changed_by_email = %q, expected editor@test.com", i, *h.ChangedByEmail)
		}
	}
	// Newest-first sort: created_at[0] >= created_at[1]. Direct string
	// compare works because RFC3339 lexicographic order = chronological.
	if env.Data[0].CreatedAt < env.Data[1].CreatedAt {
		t.Errorf("history must be newest-first (sorted by created_at desc); got %s before %s",
			env.Data[0].CreatedAt, env.Data[1].CreatedAt)
	}
}

// TestIntegrationTMListEntryHistory_CrossTMSameTenant: GET history variant
// of the cross-TM URL scoping check on the PATCH/DELETE handlers.
func TestIntegrationTMListEntryHistory_CrossTMSameTenant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "Tenant")
	user := testutil.SeedUser(t, q, tenant.ID, "user@test.com", "pass")
	tm1 := testutil.SeedTM(t, q, tenant.ID, "TM 1")
	tm2 := testutil.SeedTM(t, q, tenant.ID, "TM 2")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm2.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Lives in TM 2", TargetText: "Vive", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/tms/%d/entries/%d/history", tm1.ID, entry.ID),
		nil, user.ID, tenant.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-TM history GET must 404; got %d", rec.Code)
	}
}

// TestIntegrationTMUpdateEntry_Conflict409BodyShape pins the 409 response body
// for the side-by-side conflict banner (same shape as the CAT-side banner).
// The frontend renders "their version vs my edit" side-by-side from
// this payload — drift in the response shape silently breaks the banner.
//
// Body must contain:
//   - error.message — legacy plain-error key, kept for clients without banner support
//   - conflict.id, conflict.source_text, conflict.target_text
//   - conflict.source_rich_text, conflict.target_rich_text — for chip rendering
//   - conflict.updated_at — ISO timestamp the client uses to refetch
//   - conflict.updated_by — display label as stored in the TM row
//   - conflict.updated_by_user_id — resolved via LEFT JOIN on users.email
//     (NULL when label isn't a user email,
//     e.g. "tmx-import"; non-NULL when human-edited)
//   - conflict.updated_by_name — resolved user's display name (or "" if not joined)
//
// Anti-tautology: every field is asserted to match the value User A wrote, so a
// regression like "we returned the request body instead of the DB row" surfaces
// immediately.
func TestIntegrationTMUpdateEntry_Conflict409BodyShape(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TMConflictBannerCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tm-conflict@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "TM Conflict Banner")

	entry, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Original source", TargetText: "Original target",
		CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	staleStamp := entry.UpdatedAt.Time

	// User A saves "their version" with the fresh baseline. updated_by gets
	// stamped to the user's email per the handler's labeling rule.
	const theirSource = "their saved source"
	const theirTarget = `their <bpt id="1"><b></bpt>saved target<ept id="1"></b></ept>`
	rec := testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":         theirSource,
			"target_text":         theirTarget,
			"target_rich_text":    theirTarget,
			"expected_updated_at": staleStamp.Format(time.RFC3339Nano),
		}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// User B (still on the stale baseline) tries to save → 409 with the
	// conflict body shape.
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/tms/%d/entries/%d", tm.ID, entry.ID),
		map[string]any{
			"source_text":         "stale write attempt",
			"target_text":         "stale write attempt",
			"expected_updated_at": staleStamp.Format(time.RFC3339Nano),
		}, user.ID, tenant.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Conflict struct {
			ID              int64   `json:"id"`
			SourceText      string  `json:"source_text"`
			TargetText      string  `json:"target_text"`
			SourceRichText  *string `json:"source_rich_text"`
			TargetRichText  *string `json:"target_rich_text"`
			UpdatedAt       string  `json:"updated_at"`
			UpdatedBy       string  `json:"updated_by"`
			UpdatedByUserID *int64  `json:"updated_by_user_id"`
			UpdatedByName   string  `json:"updated_by_name"`
		} `json:"conflict"`
	}
	testutil.DecodeJSON(t, rec, &body)

	if body.Error.Message == "" {
		t.Error("legacy `error.message` missing — older clients still rely on it")
	}
	if body.Conflict.ID != entry.ID {
		t.Errorf("conflict.id = %d, want %d", body.Conflict.ID, entry.ID)
	}
	if body.Conflict.SourceText != theirSource {
		t.Errorf("conflict.source_text = %q, want %q", body.Conflict.SourceText, theirSource)
	}
	if body.Conflict.TargetText != theirTarget {
		t.Errorf("conflict.target_text = %q, want %q", body.Conflict.TargetText, theirTarget)
	}
	if body.Conflict.TargetRichText == nil || *body.Conflict.TargetRichText != theirTarget {
		t.Errorf("conflict.target_rich_text = %v, want %q", body.Conflict.TargetRichText, theirTarget)
	}
	if body.Conflict.UpdatedAt == "" || body.Conflict.UpdatedAt == staleStamp.Format(time.RFC3339Nano) {
		t.Errorf("conflict.updated_at must reflect the new server-side timestamp; got %q", body.Conflict.UpdatedAt)
	}
	// Handler stamps updated_by to user.Email; the LEFT JOIN on users.email
	// resolves it to the user record so the banner can detect same-user (id
	// match) vs different-user.
	if body.Conflict.UpdatedBy != user.Email {
		t.Errorf("conflict.updated_by = %q, want %q", body.Conflict.UpdatedBy, user.Email)
	}
	if body.Conflict.UpdatedByUserID == nil || *body.Conflict.UpdatedByUserID != user.ID {
		t.Errorf("conflict.updated_by_user_id = %v, want %d (LEFT JOIN on email)", body.Conflict.UpdatedByUserID, user.ID)
	}
	if body.Conflict.UpdatedByName != user.Name {
		t.Errorf("conflict.updated_by_name = %q, want %q", body.Conflict.UpdatedByName, user.Name)
	}
}

// TestIntegrationGetTMEntryConflictDetailsTenantScope pins the tenant boundary
// of the GetTMEntryConflictDetails query directly. The handler already verifies tenant access
// via GetTMEntryScoped before reaching the 409 branch, but every user-data
// query carries its own scope (CONTRIBUTING.md, "Tenant isolation") so a future
// caller can't accidentally bypass it.
// This test calls the query directly, NOT through the handler.
func TestIntegrationGetTMEntryConflictDetailsTenantScope(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantA := testutil.SeedTenant(t, q, "TMConflictTenantA")
	testutil.SeedUser(t, q, tenantA.ID, "a-tm-conflict@test.com", "pass")
	tmA := testutil.SeedTM(t, q, tenantA.ID, "Tenant A's TM")
	entryA, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmA.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Hello", TargetText: "Hola", CreatedBy: "test", TuID: "",
	})
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	// Tenant B exists but should NOT see tenant A's TM entry.
	tenantB := testutil.SeedTenant(t, q, "TMConflictTenantB")

	// Same-tenant lookup succeeds.
	row, err := q.GetTMEntryConflictDetails(ctx, db.GetTMEntryConflictDetailsParams{
		ID: entryA.ID, TmID: tmA.ID, TenantID: tenantA.ID,
	})
	if err != nil {
		t.Fatalf("same-tenant lookup must succeed, got %v", err)
	}
	if row.SourceText != "Hello" {
		t.Errorf("same-tenant: source_text = %q, want %q", row.SourceText, "Hello")
	}

	// Cross-tenant lookup must return ErrNoRows (sqlc's :one returns no row).
	_, err = q.GetTMEntryConflictDetails(ctx, db.GetTMEntryConflictDetailsParams{
		ID: entryA.ID, TmID: tmA.ID, TenantID: tenantB.ID,
	})
	if err == nil {
		t.Fatal("cross-tenant lookup must fail; tenant boundary leaked")
	}
}

// TestIntegrationTMMatch_ScoringContract pins the engine-agnostic contract
// for /api/jobs/{jobID}/tm-matches. Assertions are on score relationships,
// presence, and ranking — never on specific numeric scores or the
// underlying algorithm — so the suite survives an engine swap (pg_trgm →
// Levenshtein → ES → …).
func TestIntegrationTMMatch_ScoringContract(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TM-Score-Test")
	user := testutil.SeedUser(t, q, tenant.ID, "u@scoring.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Scoring Project", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	tm := testutil.SeedTM(t, q, tenant.ID, "Scoring TM")

	// 0.65 must sit BETWEEN today's pg_trgm score (~0.52 for the C3 pair)
	// and the post-fix Levenshtein score (~0.80) — that's what makes C3
	// distinguish buggy from fixed behavior. Don't tune this to make tests
	// convenient.
	sp, err := q.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetSettingsProfile: %v", err)
	}
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:             sp.ID,
		FuzzyThreshold: 0.65,
		MtOnUpload:     sp.MtOnUpload,
		TmOnUpload:     sp.TmOnUpload,
		QaProfileID:    sp.QaProfileID,
	}); err != nil {
		t.Fatalf("UpdateSettingsProfile: %v", err)
	}

	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          0,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	seedTMEntry := func(src, tgt string) {
		t.Helper()
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tm.ID,
			SourceLanguage: "en",
			TargetLanguage: "es",
			SourceText:     src,
			TargetText:     tgt,
			CreatedBy:      "test",
		}); err != nil {
			t.Fatalf("InsertTM(%q): %v", src, err)
		}
	}
	seedTMEntry("Hello world", "Hola mundo")
	seedTMEntry("Hello world!", "Hola mundo!")
	seedTMEntry("This text is in bold", "Este texto está en negrita")
	seedTMEntry("Greetings", "Saludos")

	type match struct {
		Score      float64 `json:"score"`
		SourceText string  `json:"source_text"`
	}

	fetchMatches := func(t *testing.T, sourceText string) []match {
		t.Helper()
		path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s",
			job.ID, url.QueryEscape(sourceText))
		rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Matches []match `json:"matches"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Matches
	}

	findBySource := func(matches []match, src string) (match, bool) {
		for _, m := range matches {
			if m.SourceText == src {
				return m, true
			}
		}
		return match{}, false
	}

	t.Run("C1_byte_identical_scores_one", func(t *testing.T) {
		matches := fetchMatches(t, "Hello world")
		m, ok := findBySource(matches, "Hello world")
		if !ok {
			t.Fatalf(`"Hello world" missing from matches: %+v`, matches)
		}
		if m.Score != 1.0 {
			t.Errorf(`score = %v, want 1.0 (byte-identical match defines "exact")`, m.Score)
		}
	})

	t.Run("C2_near_exact_non_byte_identical_scores_below_one", func(t *testing.T) {
		cases := []struct {
			name, query string
		}{
			{"trailing_period", "Hello world."},
			{"case_diff_all_lower", "hello world"},
			{"trailing_whitespace", "Hello world "},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				matches := fetchMatches(t, c.query)
				m, ok := findBySource(matches, "Hello world")
				if !ok {
					t.Fatalf(`query %q: "Hello world" missing from matches: %+v`, c.query, matches)
				}
				if m.Score >= 1.0 {
					t.Errorf(`query %q: score = %v, want < 1.0 (matcher must distinguish near-exact from byte-identical)`,
						c.query, m.Score)
				}
			})
		}
	})

	t.Run("C3_one_word_swap_surfaces_fuzzy_match", func(t *testing.T) {
		matches := fetchMatches(t, "This text is in italics")
		if _, ok := findBySource(matches, "This text is in bold"); !ok {
			t.Fatalf(`"This text is in bold" missing — 4-of-5-word match below threshold 0.65: %+v`, matches)
		}
	})

	t.Run("C4_unrelated_text_returns_no_matches", func(t *testing.T) {
		matches := fetchMatches(t, "Quantum entanglement protocols")
		if len(matches) != 0 {
			t.Errorf(`unrelated query returned %d match(es): %+v`, len(matches), matches)
		}
	})

	t.Run("C5_exact_ranks_above_and_is_distinguishable_from_near_exact", func(t *testing.T) {
		matches := fetchMatches(t, "Hello world")
		if len(matches) < 2 {
			t.Fatalf(`want >= 2 matches (exact + near-exact); got %d: %+v`, len(matches), matches)
		}
		if matches[0].SourceText != "Hello world" {
			t.Errorf(`first match = %q (score %v), want "Hello world"`, matches[0].SourceText, matches[0].Score)
		}
		if matches[0].Score != 1.0 {
			t.Errorf(`first match score = %v, want 1.0`, matches[0].Score)
		}
		// Distinguishability: without this, "exact ranks first" passes via
		// handler ordering + stable sort even when both candidates tie at
		// 1.0. Pins ranking-by-score, not ranking-by-call-order.
		if matches[1].Score >= 1.0 {
			t.Errorf(`second match score = %v, want < 1.0 (matcher ties exact with near-exact)`, matches[1].Score)
		}
	})
}

// TestIntegrationTMConflictDetailsResolvesUserLabel guards that the conflict
// banner join must resolve updated_by back to a user record whether the label
// is the new "Name <email>" form or a legacy bare email. Otherwise the
// optimistic-lock banner silently degrades to its anonymous heading.
func TestIntegrationTMConflictDetailsResolvesUserLabel(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ConflictCorp")
	// User with a display name distinct from the email so the "Name <email>"
	// (angle-bracket) branch of the join is actually exercised.
	user, err := q.CreateUser(ctx, db.CreateUserParams{Email: "editor@example.com", Name: "Edith Editor"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := q.CreateMembership(ctx, db.CreateMembershipParams{UserID: user.ID, TenantID: tenant.ID, Role: "owner"}); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	coll, err := q.CreateTMCollection(ctx, db.CreateTMCollectionParams{Name: "conflict-tm", TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// updated_by is set from InsertTM's created_by param (query binds both).
	for _, tc := range []struct {
		name      string
		updatedBy string
	}{
		{"name-and-email label", auth.UserLabel(user.Name, user.Email)}, // "Edith Editor <editor@example.com>"
		{"legacy bare email", user.Email},                               // legacy inline-edit rows
		// A display name that itself contains angle brackets must not fool the
		// email extraction: the email is always the LAST "<...>" group, so the
		// join regex is anchored to end-of-string. Un-anchored, this captures
		// "Team" and the join fails to resolve the user.
		{"display name containing angle brackets", auth.UserLabel("Ops <Team>", user.Email)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := q.InsertTM(ctx, db.InsertTMParams{
				TmID: coll.ID, SourceLanguage: "en", TargetLanguage: "de",
				SourceText: "Source " + tc.name, TargetText: "Ziel", CreatedBy: tc.updatedBy, TuID: "",
			})
			if err != nil {
				t.Fatalf("insert entry: %v", err)
			}
			row, err := q.GetTMEntryConflictDetails(ctx, db.GetTMEntryConflictDetailsParams{
				ID: entry.ID, TmID: coll.ID, TenantID: tenant.ID,
			})
			if err != nil {
				t.Fatalf("GetTMEntryConflictDetails: %v", err)
			}
			if row.UpdatedByUserID == nil || *row.UpdatedByUserID != user.ID {
				t.Errorf("updated_by_user_id = %v, want %d (join failed to resolve %q)", row.UpdatedByUserID, user.ID, tc.updatedBy)
			}
			if row.UpdatedByName != user.Name {
				t.Errorf("updated_by_name = %q, want %q", row.UpdatedByName, user.Name)
			}
		})
	}

	// A non-user label ('human' / import tool) must resolve to no user.
	sys, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: coll.ID, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "System source", TargetText: "Ziel", CreatedBy: "human", TuID: "",
	})
	if err != nil {
		t.Fatalf("insert system entry: %v", err)
	}
	row, err := q.GetTMEntryConflictDetails(ctx, db.GetTMEntryConflictDetailsParams{ID: sys.ID, TmID: coll.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("GetTMEntryConflictDetails (system): %v", err)
	}
	if row.UpdatedByUserID != nil {
		t.Errorf("updated_by_user_id = %v, want nil for 'human' label", *row.UpdatedByUserID)
	}
}

// The editor's match panel and the pre-translate pipeline must agree on the
// same hit: both charge the sublanguage penalty per mismatched side. If only one of
// them did, a linguist would see 100% in the sidebar for a segment the pipeline
// had filled at 98% (or vice versa).
func TestIntegrationTMMatch_SublanguageVariantIsPenalized(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()

	tenant := testutil.SeedTenant(t, q, "TM-Sublang-Test")
	user := testutil.SeedUser(t, q, tenant.ID, "u@sublang.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Sublang Project", "en-us", []string{"fr-fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr-fr")
	tm := testutil.SeedTM(t, q, tenant.ID, "Sublang TM")

	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          0,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	for _, e := range []struct{ src, tgt, text string }{
		{"en", "fr", "Bonjour le monde"},          // variant on both sides → 2 penalties
		{"en-us", "fr", "Salut le monde"},         // variant on the target only → 1 penalty
		{"en-us", "fr-fr", "Bonjour, le monde !"}, // perfect locale → unpenalized
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tm.ID,
			SourceLanguage: e.src,
			TargetLanguage: e.tgt,
			SourceText:     "Hello world",
			TargetText:     e.text,
			CreatedBy:      "test",
		}); err != nil {
			t.Fatalf("InsertTM %s→%s: %v", e.src, e.tgt, err)
		}
	}

	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Hello world"))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Matches []struct {
			Score      float64 `json:"score"`
			TargetText string  `json:"target_text"`
		} `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	scores := map[string]float64{}
	for _, m := range resp.Matches {
		scores[m.TargetText] = m.Score
	}

	want := map[string]float64{
		"Bonjour, le monde !": 1.0,
		"Salut le monde":      1.0 - translationmemory.DefaultSublanguagePenalty,
		"Bonjour le monde":    1.0 - 2*translationmemory.DefaultSublanguagePenalty,
	}
	for target, wantScore := range want {
		got, ok := scores[target]
		if !ok {
			t.Errorf("missing match %q — got %v", target, scores)
			continue
		}
		if math.Abs(got-wantScore) > 1e-9 {
			t.Errorf("match %q scored %v, want %v", target, got, wantScore)
		}
	}
}

// The editor's Matches panel must score with the linked TM profile's TMs and
// threshold. The profile's threshold (0.5) admits a fuzzy that the project's
// own 0.9 would hide, and the entry lives only in the profile's TM.
func TestIntegrationTMMatch_LinkedTMProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "TM-Profile-Match")
	user := testutil.SeedUser(t, q, tenant.ID, "u@profile.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"es"})
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{ID: project.SettingsProfileID, FuzzyThreshold: 0.9}); err != nil {
		t.Fatalf("set own threshold: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	profTM := testutil.SeedTM(t, q, tenant.ID, "Profile TM")
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Loose", 0.5)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read", Priority: 0,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: profTM.ID, SourceLanguage: "en", TargetLanguage: "es",
		SourceText: "Reset your password.", TargetText: "Restablece tu contraseña.", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	testutil.LinkTMProfile(t, q, project.SettingsProfileID, tp.ID)

	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Link to reset your password."))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Matches []struct {
			Score  float64 `json:"score"`
			TMName string  `json:"tm_name"`
		} `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Matches) != 1 || resp.Matches[0].TMName != "Profile TM" {
		t.Fatalf("want one match from the profile's TM, got %+v", resp.Matches)
	}
	if s := resp.Matches[0].Score; s < 0.5 || s >= 0.9 {
		t.Errorf("score %v must sit in [0.5, 0.9) — only the profile's threshold admits it", s)
	}
}

// tmEntries returns every entry of one TM in id order.
func tmEntries(t *testing.T, q db.Querier, tmID int64) []db.TranslationMemory {
	t.Helper()
	entries, err := q.ListTMEntriesBatch(context.Background(), db.ListTMEntriesBatchParams{TmID: tmID, BatchLimit: 10000})
	if err != nil {
		t.Fatalf("list TM entries: %v", err)
	}
	return entries
}
