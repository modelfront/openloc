package pipeline_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parsers/xliff"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// =============================================================================
// Bilingual XLIFF export from any source format
// =============================================================================
//
// The serializer's correctness is covered by the pure-function unit tests
// in internal/parsers/xliff/xliff_serialize_test.go (9 tests + 3 anti-tautology
// RED proofs). The integration tests below cover the wiring:
//   - the pipeline.ExportBilingualXLIFF service method end-to-end
//     against a real Postgres testcontainer
//   - the round-trip contract: bilingual export of a non-XLIFF source
//     can be re-imported via POST /api/files/{id}/reimport-xliff (key match)
//   - graceful behavior on edge cases (empty file, no translation rows,
//     locked + approved + untranslated mix)
//
// =============================================================================
// Anti-tautology RED proofs (durable artifact)
// =============================================================================
// Proof 1 — the trans-unit id MUST equal segment_key (round-trip contract).
//   Mutation:    pipeline/export.go::ExportBilingualXLIFF — change the
//                seg.Key assignment from `*row.SegmentKey` to a synthetic
//                string like `fmt.Sprintf("seg-%d", row.SegmentID)`.
//   Test:        TestIntegration_BilingualExport_RoundTripsViaReimport
//   Diagnostic:  the re-import would not find the row by key (silently
//                falls back to "extra in upload" or "missing in upload"
//                buckets); test asserts the segment got updated post-
//                re-import, which fails because the keys mismatch.
//   Proven:      2026-05-06.
//
// Proof 2 — locked segments emit signed-off, surviving the round-trip.
//   Mutation:    parser/xliff_serialize.go::mapStatusToXLIFFState —
//                drop the `if locked { return signed-off }` branch.
//   Test:        TestIntegration_BilingualExport_EmitsAllLifecycleStates
//   Diagnostic:  the locked row's emitted state would be "final"
//                (mapped from approved status) instead of "signed-off",
//                losing the lock signal for receiving CAT tools.
//   Proven:      2026-05-06.

// bilingualExportFixture stages a project + file + segments + translations
// for the bilingual export tests. Returns the file ID + the user's
// tenant + the segment_key→translation_id index for assertions.
type bilingualExportFixture struct {
	UserID    int64
	TenantID  int64
	ProjectID int64
	FileID    int64
}

func setupBilingualExportFixture(t *testing.T, q db.Querier, prefix string, sourceLang string, targetLangs []string) bilingualExportFixture {
	t.Helper()
	tenant := testutil.SeedTenant(t, q, prefix+" Corp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, prefix+"@test.com", "pass", "owner")
	project := testutil.SeedProject(t, q, tenant.ID, prefix+" Project", sourceLang, targetLangs)
	// Use TXT format so the test exercises non-XLIFF source — the whole
	// point of bilingual export.
	file := testutil.SeedFile(t, q, project.ID, prefix+".txt", db.FileFormatTxt)
	return bilingualExportFixture{
		UserID:    user.ID,
		TenantID:  tenant.ID,
		ProjectID: project.ID,
		FileID:    file.ID,
	}
}

// seedSegmentWithKey is the bilingual-export tests' segment helper. The
// shared `testutil.SeedSegment` doesn't set `segment_key`, but the
// bilingual export's whole correctness rests on segment_key being non-
// nil (it becomes the trans-unit id). Mimicking the TXT parser's "line:N"
// convention here keeps the test data shape consistent with what a real
// upload would produce.
func seedSegmentWithKey(t *testing.T, q db.Querier, fileID int64, key, source string, pos int32) db.Segment {
	t.Helper()
	seg, err := q.CreateSegment(context.Background(), db.CreateSegmentParams{
		FileID:     fileID,
		SourceText: source,
		SegmentKey: &key,
		Position:   pos,
	})
	if err != nil {
		t.Fatalf("seedSegmentWithKey(%q): %v", key, err)
	}
	return seg
}

// TestIntegration_BilingualExport_HappyPath verifies the wiring from
// service method through SQL through serializer for a TXT source file
// with approved + untranslated segments. Asserts:
//   - the result is parseable XLIFF (self-round-trip via xliff.XLIFF)
//   - source-language + target-language attributes are populated
//   - segment_key is preserved as trans-unit id
//   - approved segments emit state=final, untranslated emit
//     state=needs-translation
func TestIntegration_BilingualExport_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := setupBilingualExportFixture(t, q, "happy", "en", []string{"es"})

	// Two segments — one approved, one untranslated — to exercise both
	// branches of the state mapping.
	seg1 := seedSegmentWithKey(t, q, fx.FileID, "line:1", "Hello {name}", 1)
	seg2 := seedSegmentWithKey(t, q, fx.FileID, "line:2", "Goodbye", 2)

	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusApproved)
	humanText := "Hola {name}"
	if _, err := q.UpdateTranslationHuman(context.Background(), db.UpdateTranslationHumanParams{
		ID:        tr1.ID,
		HumanText: &humanText,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}
	// seg2 has no translation row at all (LEFT JOIN should produce empty target).
	_ = seg2

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	data, filename, err := svc.ExportBilingualXLIFF(context.Background(), fx.TenantID, fx.FileID, "en", "es")
	if err != nil {
		t.Fatalf("ExportBilingualXLIFF: %v", err)
	}
	if !strings.HasSuffix(filename, "_es.xlf") {
		t.Errorf("expected filename to end in _es.xlf, got %q", filename)
	}

	body := string(data)
	// `original` is a required XLIFF 1.2 <file> attribute (memoQ rejects its
	// absence). It must carry the source document's name end-to-end — the
	// fixture seeds the file as "happy.txt".
	if !strings.Contains(body, `original="happy.txt"`) {
		t.Errorf("missing original attribute carrying source filename, got body: %s", body)
	}
	if !strings.Contains(body, `source-language="en"`) {
		t.Error("missing source-language attribute")
	}
	if !strings.Contains(body, `target-language="es"`) {
		t.Error("missing target-language attribute")
	}
	if !strings.Contains(body, `<trans-unit id="line:1">`) {
		t.Error("trans-unit id MUST equal segment_key (line:1) — round-trip contract")
	}
	if !strings.Contains(body, `<source>Hello {name}</source>`) {
		t.Error("approved segment's source missing")
	}
	if !strings.Contains(body, `<target state="final">Hola {name}</target>`) {
		t.Errorf("approved segment should emit state=final + target text, got body: %s", body)
	}
	// seg2 has no translation row → status is empty/null → maps to needs-translation.
	if !strings.Contains(body, `<source>Goodbye</source>`) {
		t.Error("untranslated segment's source missing")
	}
	if !strings.Contains(body, `<target state="needs-translation"></target>`) {
		t.Errorf("untranslated segment should emit state=needs-translation with empty target, got body: %s", body)
	}

	// Self-parse: the XLIFF parser must read what we emit.
	parsed, err := xliff.XLIFF{}.Parse(data)
	if err != nil {
		t.Fatalf("self-parse: %v\nbody:\n%s", err, body)
	}
	if len(parsed) != 2 {
		t.Errorf("self-parse: expected 2 segments, got %d", len(parsed))
	}
}

// TestIntegration_BilingualExport_EmitsAllLifecycleStates pins the
// status→state mapping at the integration layer (the unit test pins it at
// the pure-function layer; this confirms the pipeline glue routes the
// right values through).
func TestIntegration_BilingualExport_EmitsAllLifecycleStates(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := setupBilingualExportFixture(t, q, "states", "en", []string{"de"})

	cases := []struct {
		key       string
		source    string
		target    string
		status    db.TranslationStatus
		locked    bool
		wantState string
	}{
		{"line:1", "Approved hello", "Genehmigt hallo", db.TranslationStatusApproved, false, "final"},
		{"line:2", "Auto-approved", "Auto", db.TranslationStatusAutoApproved, false, "final"},
		{"line:3", "Pre-translated", "MT-Bonjour", db.TranslationStatusNeedsReview, false, "needs-review-translation"},
		{"line:4", "Needs review", "Bedarf Review", db.TranslationStatusNeedsReview, false, "needs-review-translation"},
		{"line:5", "Untranslated", "", db.TranslationStatusNeedsReview, false, "needs-translation"},
		{"line:6", "Locked approved", "Gesperrt", db.TranslationStatusApproved, true, "signed-off"},
	}

	for i, c := range cases {
		seg := seedSegmentWithKey(t, q, fx.FileID, c.key, c.source, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "de", c.status)

		// For target text — bypass UpdateTranslationHuman because it
		// auto-promotes to status="approved" (correct in production,
		// destructive in this test which needs to PIN the
		// pre_translated/needs_review states alongside their text).
		// Direct SQL keeps each row at the test's intended status.
		if c.target != "" {
			// active_text is the canonical visible target across all
			// statuses. The export
			// reads active_text; status carries the lifecycle state.
			if _, err := pool.Exec(ctx,
				`UPDATE translations SET active_text = $1 WHERE id = $2`,
				c.target, tr.ID,
			); err != nil {
				t.Fatalf("seed translation target[%d]: %v", i, err)
			}
		}
		if c.locked {
			if err := q.LockTranslation(ctx, tr.ID); err != nil {
				t.Fatalf("LockTranslation[%d]: %v", i, err)
			}
		}
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	data, _, err := svc.ExportBilingualXLIFF(ctx, fx.TenantID, fx.FileID, "en", "de")
	if err != nil {
		t.Fatalf("ExportBilingualXLIFF: %v", err)
	}
	body := string(data)

	for _, c := range cases {
		// Look for the specific trans-unit and verify its state attribute.
		marker := fmt.Sprintf(`<trans-unit id="%s">`, c.key)
		idx := strings.Index(body, marker)
		if idx < 0 {
			t.Errorf("trans-unit id=%q missing from output", c.key)
			continue
		}
		// Search forward from this trans-unit start for the next state= attribute.
		// (it's the target's state). Window-bound to the next </trans-unit>.
		end := strings.Index(body[idx:], "</trans-unit>")
		if end < 0 {
			t.Errorf("malformed XLIFF — no </trans-unit> after %s", c.key)
			continue
		}
		window := body[idx : idx+end]
		want := fmt.Sprintf(`state="%s"`, c.wantState)
		if !strings.Contains(window, want) {
			t.Errorf("trans-unit %s: want %s in window, got: %s", c.key, want, window)
		}
	}
}

// TestIntegration_BilingualExport_RoundTripsViaReimport is the
// load-bearing structural test. The bilingual export's whole purpose
// is to hand a file to an external translator and re-import what they
// send back. The contract is: trans-unit id MUST equal
// segments.segment_key, so the re-import path finds the match by key.
//
// This structural test:
//  1. Create a TXT-source file with two segments.
//  2. Export bilingual XLIFF.
//  3. Re-parse it with the same parser the re-import path uses.
//  4. Programmatically modify a `<target>` (simulate translator edit).
//  5. Re-parse and confirm the edit survives.
//
// The full HTTP round-trip (export endpoint → re-import endpoint with
// DB state assertion) is covered by `TestIntegration_BilingualExport_HTTPRoundTrip`
// below — this structural variant catches contract regressions earlier
// in the test suite (no testcontainer cycling for HTTP setup).
func TestIntegration_BilingualExport_RoundTripsViaReimport(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := setupBilingualExportFixture(t, q, "roundtrip", "en", []string{"fr"})

	seg1 := seedSegmentWithKey(t, q, fx.FileID, "line:1", "Hello", 1)
	seg2 := seedSegmentWithKey(t, q, fx.FileID, "line:2", "World", 2)
	_ = seg1
	_ = seg2

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	data, _, err := svc.ExportBilingualXLIFF(ctx, fx.TenantID, fx.FileID, "en", "fr")
	if err != nil {
		t.Fatalf("ExportBilingualXLIFF: %v", err)
	}

	// Parse what we emitted with the same parser the re-import path uses.
	// If the keys match, this is sufficient evidence the round-trip works
	// at the structural level. (The actual SQL update path is covered by
	// the re-import tests in internal/file.)
	segments, err := xliff.XLIFF{}.Parse(data)
	if err != nil {
		t.Fatalf("re-parse the bilingual export: %v\noutput:\n%s", err, string(data))
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments after parse, got %d", len(segments))
	}
	gotKeys := []string{segments[0].Key, segments[1].Key}
	wantKeys := []string{"line:1", "line:2"}
	for i, want := range wantKeys {
		if gotKeys[i] != want {
			t.Errorf("segment[%d]: emitted key=%q, want %q (round-trip would fail)", i, gotKeys[i], want)
		}
	}

	// Verify that programmatically modifying the export's <target> and
	// re-parsing yields the new text — this is what an external translator
	// would do in their tool. The parser correctly extracts <target>
	// content (also covered by the re-import tests, pinned here for the
	// bilingual code path specifically).
	modified := strings.Replace(string(data),
		`<target state="needs-translation"></target>`,
		`<target state="needs-translation">Bonjour</target>`, 1)
	reparsed, err := xliff.XLIFF{}.Parse([]byte(modified))
	if err != nil {
		t.Fatalf("re-parse after programmatic edit: %v", err)
	}
	if len(reparsed) != 2 {
		t.Fatalf("re-parse: expected 2 segments, got %d", len(reparsed))
	}
	if reparsed[0].Target != "Bonjour" {
		t.Errorf("translator's edit didn't survive re-parse: target[0]=%q, want %q", reparsed[0].Target, "Bonjour")
	}
}

// TestIntegration_BilingualExport_SourceRichTextPreservedInOutput
// verifies that XLIFF inline tags from the source survive the export.
// Without this, a recipient CAT tool would see a tagless source and
// the translator couldn't insert the matching tag on the target side.
//
// CreateSegment doesn't accept source_rich_text (the column is filled
// only by the parser-driven bulk-insert path in internal/file/handler.go).
// Using raw SQL via the pool is the cleanest test-only way to seed the
// column without adding a sqlc query just for tests.
func TestIntegration_BilingualExport_SourceRichTextPreservedInOutput(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := setupBilingualExportFixture(t, q, "richtext", "en", []string{"es"})

	rich := `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept>`
	if _, err := pool.Exec(ctx,
		`INSERT INTO segments (file_id, source_text, segment_key, position, context, word_count, source_rich_text)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		fx.FileID, "Click here", "line:1", 0, "", 2, rich,
	); err != nil {
		t.Fatalf("seed segment with rich text: %v", err)
	}
	_ = q

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	data, _, err := svc.ExportBilingualXLIFF(ctx, fx.TenantID, fx.FileID, "en", "es")
	if err != nil {
		t.Fatalf("ExportBilingualXLIFF: %v", err)
	}

	body := string(data)
	if !strings.Contains(body, `<source>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept></source>`) {
		t.Errorf("source rich text not preserved verbatim, got body: %s", body)
	}

	// And the document is well-formed XML — defensive smoke check that
	// emitting the rich-text verbatim didn't break the surrounding
	// structure. Explicitly distinguish io.EOF (well-formed) from any
	// other error (malformed); without the EOF assertion the loop would
	// accept any error — including parse failures — as loop termination.
	dec := xml.NewDecoder(strings.NewReader(body))
	var lastErr error
	for {
		_, err := dec.Token()
		if err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, io.EOF) {
		t.Errorf("emitted XLIFF is not well-formed: parser stopped with %v (want io.EOF)\nbody:\n%s", lastErr, body)
	}
}

// TestIntegration_BilingualExport_HTTPRoundTrip exercises the full
// bilingual-export → translator-edit → re-import workflow end-to-end
// through the HTTP layer. `RoundTripsViaReimport` above tests the structural contract via
// parser self-parse; this one tests the real workflow against the
// HTTP endpoints used in production:
//
//  1. Register an admin user (re-import is admin-gated).
//  2. Create a project + TXT-source file + segments.
//  3. GET /api/files/{id}/export?format=xliff → bilingual XLIFF bytes.
//  4. Programmatically modify a `<target>` (translator-tool simulation).
//  5. POST /api/files/{id}/reimport-xliff (multipart, admin role).
//  6. Assert DB state shows the translation was applied via the
//     re-import path with segment_key matching.
//
// This is the test that would break loudly if the trans-unit `id` ever
// drifts from `segments.segment_key`, even with the structural test
// passing. Two ends, one round-trip, full HTTP plumbing.
func TestIntegration_BilingualExport_HTTPRoundTrip(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)

	// Seed an owner + tenant — re-import requires auth.RequireRole("owner","admin").
	userID, tenantID := testutil.RegisterUser(t, db.New(pool), "rt")

	// Create the project.
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "Round-Trip Project",
		"source_language":  "en",
		"target_languages": []string{"fr"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// Seed file + segments with explicit keys (TXT source — the
	// bilingual-export's primary use case). Use raw SQL because
	// SeedSegment doesn't accept segment_key, and CreateSegmentParams
	// without it produces NULL keys (which hard-fail the export — perfect
	// for proving the contract).
	var fileID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO files (project_id, name, format, storage_path) VALUES ($1, 'rt.txt', 'txt', 'rt.txt') RETURNING id`,
		projectID).Scan(&fileID); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	type segIDs struct{ s1, s2 int64 }
	var ids segIDs
	if err := pool.QueryRow(ctx,
		`INSERT INTO segments (file_id, source_text, segment_key, position, context, word_count)
		 VALUES ($1, 'Hello', 'line:1', 0, '', 1) RETURNING id`,
		fileID).Scan(&ids.s1); err != nil {
		t.Fatalf("seed segment 1: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO segments (file_id, source_text, segment_key, position, context, word_count)
		 VALUES ($1, 'World', 'line:2', 1, '', 1) RETURNING id`,
		fileID).Scan(&ids.s2); err != nil {
		t.Fatalf("seed segment 2: %v", err)
	}
	// Re-import only UPDATES existing translation rows (the diff in
	// internal/file/reimport.go classifies missing rows as "extra in upload"
	// and skips them). We pre-seed translations for both languages so
	// the re-import has rows to mutate. In production, translations get
	// created by the pre-translate pipeline or by the first save —
	// either way they exist before re-import is invoked.
	if _, err := pool.Exec(ctx,
		`INSERT INTO translations (segment_id, target_language, status)
		 VALUES ($1, 'fr', 'needs_review'), ($2, 'fr', 'needs_review')`,
		ids.s1, ids.s2); err != nil {
		t.Fatalf("seed translations: %v", err)
	}

	// Step 3: bilingual export via HTTP.
	exportPath := fmt.Sprintf("/api/files/%d/export?lang=fr&format=xliff", fileID)
	rec = testutil.DoRequest(t, router, "GET", exportPath, nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/x-xliff+xml" {
		t.Errorf("Content-Type: got %q, want application/x-xliff+xml", got)
	}
	exportedXML := rec.Body.String()
	if !strings.Contains(exportedXML, `<trans-unit id="line:1">`) {
		t.Fatalf("exported XLIFF missing line:1 trans-unit:\n%s", exportedXML)
	}

	// Step 4: simulate a translator filling in <target> for line:1 only —
	// programmatic edit. Both targets initially `<target state="needs-translation"></target>`.
	// Replace ONLY the first to test partial round-trip.
	editedXML := strings.Replace(exportedXML,
		`<target state="needs-translation"></target>`,
		`<target state="final">Bonjour</target>`, 1)
	if editedXML == exportedXML {
		t.Fatal("translator-edit simulation failed — empty target placeholder not found")
	}

	// Step 5: re-import the edited XLIFF via the HTTP endpoint.
	reimportPath := fmt.Sprintf("/api/files/%d/reimport-xliff?lang=fr", fileID)
	rec = testutil.DoFileUploadWithRole(t, router, reimportPath, "edited.xliff", editedXML, userID, tenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Step 6: assert the DB row was updated by re-import. The segment_key
	// match is what bound the round-trip together — if it failed, the
	// re-import would have dropped the change as "extra in upload" with
	// zero updates. The reimported edit lands in active_text.
	var activeText *string
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT t.active_text, t.status::text
		 FROM translations t JOIN segments s ON s.id = t.segment_id
		 WHERE s.segment_key = 'line:1' AND s.file_id = $1 AND t.target_language = 'fr'`,
		fileID,
	).Scan(&activeText, &status); err != nil {
		t.Fatalf("DB readback: %v", err)
	}
	if activeText == nil || *activeText != "Bonjour" {
		t.Errorf("round-trip lost the translator's edit: active_text=%v, want %q", activeText, "Bonjour")
	}
	if status != "needs_review" {
		t.Errorf("re-import should set status=needs_review, got %q", status)
	}
}
