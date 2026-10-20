package translation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// registerUser seeds an owner + tenant and returns userID, tenantID (there
// is no self-service registration endpoint).
func registerUser(t *testing.T, q db.Querier, prefix string) (int64, int64) {
	t.Helper()
	return testutil.RegisterUser(t, q, prefix)
}

// createProject is a helper that creates a project via API and returns the project ID.
func createProject(t *testing.T, router http.Handler, name string, userID, tenantID int64) int64 {
	t.Helper()
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             name,
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data.ID
}

// TestIntegrationPropagate tests auto-propagation of a translation to all segments
// with the same source text in a file.
func TestIntegrationPropagate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "propagate")
	projectID := createProject(t, router, "Propagate Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "propagate.txt", db.FileFormatTxt)

	// Create 3 segments with the same source text
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Hello world", 2)
	seg3 := testutil.SeedSegment(t, q, file.ID, "Hello world", 3)

	// Create translations for each segment
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusNeedsReview)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusNeedsReview)
	tr3 := testutil.SeedTranslation(t, q, seg3.ID, "es", db.TranslationStatusNeedsReview)

	// Save one translation via UpdateTranslationHuman (sets human_text + active_text)
	humanText := "Hola mundo"
	_, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr1.ID,
		HumanText: &humanText,
	})
	if err != nil {
		t.Fatalf("update translation human: %v", err)
	}

	// Propagate from tr1 to all others with same source
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Hello world",
		"target_language": "es",
		"human_text":      "Hola mundo",
		"exclude_id":      tr1.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Propagated int `json:"propagated"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Propagated != 2 {
		t.Fatalf("expected propagated=2, got %d", resp.Data.Propagated)
	}

	// Verify via DB that tr2 and tr3 have human_text set
	updated2, err := q.GetTranslation(ctx, tr2.ID)
	if err != nil {
		t.Fatalf("get translation 2: %v", err)
	}
	if updated2.ActiveText == nil || *updated2.ActiveText != "Hola mundo" {
		t.Errorf("expected tr2 human_text='Hola mundo', got %v", updated2.ActiveText)
	}

	updated3, err := q.GetTranslation(ctx, tr3.ID)
	if err != nil {
		t.Fatalf("get translation 3: %v", err)
	}
	if updated3.ActiveText == nil || *updated3.ActiveText != "Hola mundo" {
		t.Errorf("expected tr3 human_text='Hola mundo', got %v", updated3.ActiveText)
	}

	// tr1 is the excluded source: its text is unchanged and origin stays
	// NULL (the origin enum records automation lineage only, and tr1 has no
	// pipeline event in this test).
	updated1, err := q.GetTranslation(ctx, tr1.ID)
	if err != nil {
		t.Fatalf("get translation 1: %v", err)
	}
	if updated1.ActiveText == nil || *updated1.ActiveText != "Hola mundo" {
		t.Errorf("expected tr1 human_text='Hola mundo' (unchanged by propagation), got %v", updated1.ActiveText)
	}
	if updated1.Origin.Valid {
		t.Errorf("expected tr1 origin=NULL (no automation lineage in this test), got %q", updated1.Origin.TranslationOrigin)
	}
}

// TestIntegrationPropagatePreservesXLIFFTags pins that propagation copies
// the rich (tagged) form to identical-source siblings, so a Cmd+Enter on a
// tagged segment keeps every tag chip on its clones.
//
// Anti-tautology: revert the SQL to the plain-text-only form and this test
// regresses with `human_rich_text = nil, want "<bpt id=\"1\">..."`.
func TestIntegrationPropagatePreservesXLIFFTags(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "propagate-tags")
	projectID := createProject(t, router, "Propagate Tags Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "propagate-tags.txt", db.FileFormatTxt)

	// Two segments with identical source text containing inline tags.
	const taggedSrc = `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept>`
	seg1 := testutil.SeedSegment(t, q, file.ID, taggedSrc, 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, taggedSrc, 2)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusNeedsReview)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusNeedsReview)

	const humanText = `Haga clic aquí`
	const humanRichText = `Haga <bpt id="1"><b></bpt>clic aquí<ept id="1"></b></ept>`
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:            tr1.ID,
		HumanText:     ptr.To(humanText),
		HumanRichText: ptr.To(humanRichText),
	}); err != nil {
		t.Fatalf("seed update tr1: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     taggedSrc,
		"target_language": "es",
		"human_text":      humanText,
		"human_rich_text": humanRichText,
		"exclude_id":      tr1.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	updated, err := q.GetTranslation(ctx, tr2.ID)
	if err != nil {
		t.Fatalf("get tr2: %v", err)
	}
	if updated.ActiveText == nil || *updated.ActiveText != humanText {
		t.Errorf("propagated human_text = %v, want %q", updated.ActiveText, humanText)
	}
	// THE invariant this test pins: the propagated row carries the rich form
	// with chips intact, not just the plain text.
	if updated.ActiveRichText == nil {
		t.Fatal("propagated human_rich_text is nil; tags were lost")
	}
	if *updated.ActiveRichText != humanRichText {
		t.Errorf("propagated human_rich_text = %q, want %q", *updated.ActiveRichText, humanRichText)
	}
	// active_rich_text mirror so the next read in the editor finds chips
	// without falling back to active_text → plain.
	if updated.ActiveRichText == nil || *updated.ActiveRichText != humanRichText {
		t.Errorf("propagated active_rich_text = %v, want %q", updated.ActiveRichText, humanRichText)
	}
}

// TestIntegrationPropagateNoMatches tests propagation when the source text only appears once.
func TestIntegrationPropagateNoMatches(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "prop-nomatch")
	projectID := createProject(t, router, "No Match Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "nomatch.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Unique text", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	humanText := "Texto unico"
	_, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr.ID,
		HumanText: &humanText,
	})
	if err != nil {
		t.Fatalf("update translation human: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Unique text",
		"target_language": "es",
		"human_text":      "Texto unico",
		"exclude_id":      tr.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Propagated int `json:"propagated"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Propagated != 0 {
		t.Fatalf("expected propagated=0, got %d", resp.Data.Propagated)
	}
}

// TestIntegrationPropagateSkipsLockedRows pins that confirming a repetition
// never writes a locked sibling: its text, status and QA findings stay, and it
// is not in the response. The unlocked sibling is still updated.
func TestIntegrationPropagateSkipsLockedRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "prop-locked")
	projectID := createProject(t, router, "Propagate Locked Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "locked.txt", db.FileFormatTxt)

	src := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, file.ID, "Hello", 1).ID, "es", db.TranslationStatusNeedsReview)
	open := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, file.ID, "Hello", 2).ID, "es", db.TranslationStatusNeedsReview)
	locked := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, file.ID, "Hello", 3).ID, "es", db.TranslationStatusNeedsReview)

	// Draft text in needs_review: propagation writes approved, so a write
	// would show in the status as well as the text.
	lockedText := "Hola, estimado cliente"
	if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = $2 WHERE id = $1`, locked.ID, lockedText); err != nil {
		t.Fatalf("set locked translation text: %v", err)
	}
	before, err := q.GetTranslation(ctx, locked.ID)
	if err != nil {
		t.Fatalf("get locked translation: %v", err)
	}
	if err := q.LockTranslation(ctx, locked.ID); err != nil {
		t.Fatalf("lock translation: %v", err)
	}
	qaMessage := "Locked row finding"
	if _, err := pool.Exec(ctx,
		`INSERT INTO qa_results (translation_id, check_type, severity, message) VALUES ($1, 'number_consistency', 'warning', $2)`,
		locked.ID, qaMessage,
	); err != nil {
		t.Fatalf("seed qa result: %v", err)
	}

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Hello",
		"target_language": "es",
		"human_text":      "Hola",
		"exclude_id":      src.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Propagated int `json:"propagated"`
			Siblings   []struct {
				ID int64 `json:"id"`
			} `json:"siblings"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Propagated != 1 || len(resp.Data.Siblings) != 1 || resp.Data.Siblings[0].ID != open.ID {
		t.Fatalf("want only the unlocked sibling %d propagated, got propagated=%d siblings=%+v", open.ID, resp.Data.Propagated, resp.Data.Siblings)
	}

	gotOpen, err := q.GetTranslation(ctx, open.ID)
	if err != nil {
		t.Fatalf("get unlocked sibling: %v", err)
	}
	if gotOpen.ActiveText == nil || *gotOpen.ActiveText != "Hola" {
		t.Errorf("unlocked sibling active_text = %v, want Hola", gotOpen.ActiveText)
	}

	after, err := q.GetTranslation(ctx, locked.ID)
	if err != nil {
		t.Fatalf("get locked translation after: %v", err)
	}
	if after.ActiveText == nil || *after.ActiveText != lockedText {
		t.Errorf("locked sibling active_text = %v, want %q", after.ActiveText, lockedText)
	}
	if before.Status != db.TranslationStatusNeedsReview || after.Status != before.Status {
		t.Errorf("locked sibling status = %q (was %q), want it to stay %q", after.Status, before.Status, db.TranslationStatusNeedsReview)
	}
	var findings int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM qa_results WHERE translation_id = $1`, locked.ID).Scan(&findings); err != nil {
		t.Fatalf("count qa results: %v", err)
	}
	if findings != 1 {
		t.Errorf("locked sibling has %d QA findings, want its 1 finding kept", findings)
	}
}

// TestIntegrationFindReplaceCaseSensitive tests case-sensitive find & replace across job translations.
func TestIntegrationFindReplaceCaseSensitive(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "fr-case")
	projectID := createProject(t, router, "FR Case Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "findreplace.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Create 3 segments with translations that have active_text set
	seg1 := testutil.SeedSegment(t, q, file.ID, "Greeting", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Farewell", 2)
	seg3 := testutil.SeedSegment(t, q, file.ID, "Other", 3)

	activeText1 := "Hello World"
	activeText2 := "Hello Friend"
	activeText3 := "Goodbye"

	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	_, err := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg1.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText1, Origin: origin,
	})
	if err != nil {
		t.Fatalf("create translation 1: %v", err)
	}
	_, err = q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg2.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText2, Origin: origin,
	})
	if err != nil {
		t.Fatalf("create translation 2: %v", err)
	}
	_, err = q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg3.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText3, Origin: origin,
	})
	if err != nil {
		t.Fatalf("create translation 3: %v", err)
	}

	// Case-sensitive find-replace: "Hello" → "Hola"
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "Hello",
		"replace":        "Hola",
		"case_sensitive": true,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Replaced int `json:"replaced"`
			Segments int `json:"segments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Replaced != 2 {
		t.Errorf("expected replaced=2, got %d", resp.Data.Replaced)
	}
	if resp.Data.Segments != 2 {
		t.Errorf("expected segments=2, got %d", resp.Data.Segments)
	}

	// Verify "Goodbye" was not touched (no "Hello" in it)
	translations, err := q.ListTranslationsByFile(testutil.Ctx(), db.ListTranslationsByFileParams{
		FileID: file.ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	for _, tr := range translations {
		if tr.SegmentID == seg3.ID {
			// seg3 ('Goodbye') has no 'Hello' match, so
			// find-replace leaves its active_text unchanged at the seeded value.
			if tr.ActiveText == nil || *tr.ActiveText != "Goodbye" {
				t.Errorf("expected seg3 active_text unchanged ('Goodbye'), got %v", tr.ActiveText)
			}
		}
	}

	_ = job
}

// TestIntegrationFindReplaceCaseInsensitive tests case-insensitive find & replace.
func TestIntegrationFindReplaceCaseInsensitive(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "fr-nocase")
	projectID := createProject(t, router, "FR NoCase Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "nocase.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	seg1 := testutil.SeedSegment(t, q, file.ID, "Greeting", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Farewell", 2)

	// Use lowercase "hello" in translation text
	activeText1 := "hello world"
	activeText2 := "HELLO friend"

	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	_, err := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg1.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText1, Origin: origin,
	})
	if err != nil {
		t.Fatalf("create translation 1: %v", err)
	}
	_, err = q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg2.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText2, Origin: origin,
	})
	if err != nil {
		t.Fatalf("create translation 2: %v", err)
	}

	// Case-insensitive find-replace: "Hello" should match "hello" and "HELLO"
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "Hello",
		"replace":        "Hola",
		"case_sensitive": false,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Replaced int `json:"replaced"`
			Segments int `json:"segments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Replaced != 2 {
		t.Errorf("expected replaced=2, got %d", resp.Data.Replaced)
	}
	if resp.Data.Segments != 2 {
		t.Errorf("expected segments=2, got %d", resp.Data.Segments)
	}

	// Verify the replacements happened
	translations, err := q.ListTranslationsByFile(testutil.Ctx(), db.ListTranslationsByFileParams{
		FileID: file.ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	for _, tr := range translations {
		if tr.ActiveText == nil {
			t.Errorf("expected human_text to be set after find-replace, segment_id=%d", tr.SegmentID)
			continue
		}
		if tr.SegmentID == seg1.ID && *tr.ActiveText != "Hola world" {
			t.Errorf("seg1: expected 'Hola world', got %q", *tr.ActiveText)
		}
		if tr.SegmentID == seg2.ID && *tr.ActiveText != "Hola friend" {
			t.Errorf("seg2: expected 'Hola friend', got %q", *tr.ActiveText)
		}
	}

	_ = job
}

// TestIntegrationFindReplaceFindMode tests that mode="find" returns match counts WITHOUT updating the DB.
func TestIntegrationFindReplaceFindMode(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "fr-find")
	projectID := createProject(t, router, "FR Find Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "findmode.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	seg1 := testutil.SeedSegment(t, q, file.ID, "Greeting", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Farewell", 2)
	seg3 := testutil.SeedSegment(t, q, file.ID, "Other", 3)

	activeText1 := "Hello World"
	activeText2 := "Hello Friend"
	activeText3 := "Goodbye"

	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg1.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText1, Origin: origin,
	})
	q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg2.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText2, Origin: origin,
	})
	q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg3.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &activeText3, Origin: origin,
	})

	// Find mode: should return matches=2, segments=2 WITHOUT updating anything
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "Hello",
		"mode":           "find",
		"case_sensitive": true,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Matches  int `json:"matches"`
			Segments int `json:"segments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Matches != 2 {
		t.Errorf("expected matches=2, got %d", resp.Data.Matches)
	}
	if resp.Data.Segments != 2 {
		t.Errorf("expected segments=2, got %d", resp.Data.Segments)
	}

	// Also test find with 0 matches — should return matches=0, not error
	recZero := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "NONEXISTENT_TEXT_xyz",
		"mode":           "find",
		"case_sensitive": true,
	}, userID, tenantID)
	testutil.AssertStatus(t, recZero, http.StatusOK)
	var respZero struct {
		Data struct {
			Matches  int `json:"matches"`
			Segments int `json:"segments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, recZero, &respZero)
	if respZero.Data.Matches != 0 {
		t.Errorf("expected matches=0 for nonexistent text, got %d", respZero.Data.Matches)
	}

	// CRITICAL: verify DB was NOT modified (find-only, no replace).
	// Segments were seeded with active_text ("Hello
	// World" / "Hello Friend" / "Goodbye"); find-mode must leave those
	// untouched (it doesn't replace "Hello" with anything).
	translations, err := q.ListTranslationsByFile(testutil.Ctx(), db.ListTranslationsByFileParams{
		FileID: file.ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	seeded := map[int64]string{seg1.ID: "Hello World", seg2.ID: "Hello Friend", seg3.ID: "Goodbye"}
	for _, tr := range translations {
		want := seeded[tr.SegmentID]
		if tr.ActiveText == nil || *tr.ActiveText != want {
			t.Errorf("find mode must NOT modify active_text; segment_id=%d want %q got %v", tr.SegmentID, want, tr.ActiveText)
		}
	}

	_ = job
}

// TestIntegrationFindReturnsTranslationIDs verifies "find" mode returns the IDs of matching translations.
func TestIntegrationFindReturnsTranslationIDs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "fr-ids")
	projectID := createProject(t, router, "FR IDs Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "findids.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	seg1 := testutil.SeedSegment(t, q, file.ID, "Greeting", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Farewell", 2)
	seg3 := testutil.SeedSegment(t, q, file.ID, "Other", 3)

	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	at1 := "Hello World"
	at2 := "Hello Friend"
	at3 := "Goodbye"
	tr1, _ := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg1.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &at1, Origin: origin,
	})
	tr2, _ := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg2.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &at2, Origin: origin,
	})
	q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg3.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &at3, Origin: origin,
	})

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "Hello",
		"mode":           "find",
		"case_sensitive": true,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Matches        int     `json:"matches"`
			Segments       int     `json:"segments"`
			TranslationIDs []int64 `json:"translation_ids"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	if resp.Data.Matches != 2 {
		t.Errorf("expected matches=2, got %d", resp.Data.Matches)
	}
	if len(resp.Data.TranslationIDs) != 2 {
		t.Fatalf("expected 2 translation_ids, got %d", len(resp.Data.TranslationIDs))
	}
	// IDs should be the translations that contain "Hello" (tr1 and tr2)
	ids := map[int64]bool{resp.Data.TranslationIDs[0]: true, resp.Data.TranslationIDs[1]: true}
	if !ids[tr1.ID] || !ids[tr2.ID] {
		t.Errorf("expected translation IDs %d and %d, got %v", tr1.ID, tr2.ID, resp.Data.TranslationIDs)
	}

	_ = job
}

// TestIntegrationReplaceOne verifies "replace_one" mode replaces only the specified translation.
func TestIntegrationReplaceOne(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "fr-one")
	projectID := createProject(t, router, "FR One Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "replaceone.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	seg1 := testutil.SeedSegment(t, q, file.ID, "Greeting", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Farewell", 2)

	origin := db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginMt, Valid: true}
	at1 := "Hello World"
	at2 := "Hello Friend"
	tr1, _ := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg1.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &at1, Origin: origin,
	})
	tr2, _ := q.CreateTranslationWithOrigin(testutil.Ctx(), db.CreateTranslationWithOriginParams{
		SegmentID: seg2.ID, TargetLanguage: "es", Status: db.TranslationStatusNeedsReview,
		ActiveText: &at2, Origin: origin,
	})

	// Replace "Hello" → "Hola" only in tr1
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/find-replace", job.ID), map[string]any{
		"find":           "Hello",
		"replace":        "Hola",
		"mode":           "replace_one",
		"translation_id": tr1.ID,
		"case_sensitive": true,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			Replaced int `json:"replaced"`
			Segments int `json:"segments"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Replaced != 1 {
		t.Errorf("expected replaced=1, got %d", resp.Data.Replaced)
	}
	if resp.Data.Segments != 1 {
		t.Errorf("expected segments=1, got %d", resp.Data.Segments)
	}

	// Verify tr1 was replaced
	translations, err := q.ListTranslationsByFile(testutil.Ctx(), db.ListTranslationsByFileParams{
		FileID: file.ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	for _, tr := range translations {
		if tr.ID == tr1.ID {
			if tr.ActiveText == nil || *tr.ActiveText != "Hola World" {
				t.Errorf("tr1 should be 'Hola World', got %v", tr.ActiveText)
			}
		}
		if tr.ID == tr2.ID {
			// tr2 should NOT be modified — still the seeded active_text.
			if tr.ActiveText == nil || *tr.ActiveText != "Hello Friend" {
				t.Errorf("tr2 should be untouched ('Hello Friend'), got %v", tr.ActiveText)
			}
		}
	}

	_ = job
}

// TestIntegrationConcordanceSearch tests concordance search across TMs assigned to a template.
func TestIntegrationConcordanceSearch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "concordance")

	// Create template via API
	rec := testutil.DoRequest(t, router, "POST", "/api/templates", map[string]any{
		"name": "Concordance Template",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var tmplResp struct {
		Data struct {
			ID                int64 `json:"id"`
			SettingsProfileID int64 `json:"settings_profile_id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tmplResp)
	templateID := tmplResp.Data.ID
	templateSPID := tmplResp.Data.SettingsProfileID

	// Create TM collection
	tm := testutil.SeedTM(t, q, tenantID, "Concordance TM")

	// Add TM entries
	_, err := q.InsertTM(testutil.Ctx(), db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "good morning everyone",
		TargetText:     "buenos dias a todos",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("upsert TM entry 1: %v", err)
	}
	_, err = q.InsertTM(testutil.Ctx(), db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "good evening",
		TargetText:     "buenas noches",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("upsert TM entry 2: %v", err)
	}
	_, err = q.InsertTM(testutil.Ctx(), db.InsertTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "unrelated phrase",
		TargetText:     "frase no relacionada",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("upsert TM entry 3: %v", err)
	}

	// Assign TM to template's settings profile
	_, err = q.CreateSettingsTMAssignment(testutil.Ctx(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: templateSPID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("create template TM assignment: %v", err)
	}

	// Search for "morning" — should match "good morning everyone"
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/tms/concordance?template_id=%d&source_language=en&target_language=es&q=morning", templateID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Concordance handler returns {tm_matches: [...], term_matches: [...]} —
	// no `data` envelope (unlike most other endpoints).
	var searchResp struct {
		TmMatches []struct {
			ID         int64  `json:"id"`
			SourceText string `json:"source_text"`
			TargetText string `json:"target_text"`
			TmName     string `json:"tm_name"`
		} `json:"tm_matches"`
	}
	testutil.DecodeJSON(t, rec, &searchResp)

	if len(searchResp.TmMatches) != 1 {
		t.Fatalf("expected 1 concordance result, got %d", len(searchResp.TmMatches))
	}
	if searchResp.TmMatches[0].SourceText != "good morning everyone" {
		t.Errorf("expected source_text='good morning everyone', got %q", searchResp.TmMatches[0].SourceText)
	}
	if searchResp.TmMatches[0].TargetText != "buenos dias a todos" {
		t.Errorf("expected target_text='buenos dias a todos', got %q", searchResp.TmMatches[0].TargetText)
	}
	if searchResp.TmMatches[0].TmName != "Concordance TM" {
		t.Errorf("expected tm_name='Concordance TM', got %q", searchResp.TmMatches[0].TmName)
	}

	// Search for "good" — should match both "good morning everyone" and "good evening"
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/tms/concordance?template_id=%d&source_language=en&target_language=es&q=good", templateID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	testutil.DecodeJSON(t, rec, &searchResp)
	if len(searchResp.TmMatches) != 2 {
		t.Fatalf("expected 2 concordance results for 'good', got %d", len(searchResp.TmMatches))
	}
}

func TestIntegrationLockedSegmentRejectsSave(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "locktest")
	projectID := createProject(t, router, "Lock Test Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "locktest.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Test segment", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	// Lock the translation
	err := q.LockTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("lock translation: %v", err)
	}

	// Attempt to save human text on locked segment — should be rejected (409 Conflict)
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Should be blocked", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Attempt to save draft on locked segment — should also be rejected
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
		map[string]any{"human_text": "Draft should also fail", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Unlock and verify save works
	err = q.UnlockTranslation(ctx, tr.ID)
	if err != nil {
		t.Fatalf("unlock translation: %v", err)
	}

	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Now it should work", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationTranslationSave_StaleUpdatedAtReturns409 verifies Section 5.1
// optimistic locking. Two users editing the same segment:
//
//  1. User A fetches translation (captures updated_at = T0)
//  2. User A saves human_text "first save"  → server writes, updated_at = T1
//  3. User B (still holding T0) tries to save human_text "second save" with
//     expected_updated_at = T0
//  4. Server detects T0 ≠ T1, returns 409 Conflict with the current state,
//     so user B can reload to see user A's edit instead of overwriting it.
func TestIntegrationTranslationSave_StaleUpdatedAtReturns409(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "staleput")
	projectID := createProject(t, router, "Stale Update Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "stale.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Source segment", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	// --- Step 1: both users fetch the translation. T0 is the shared starting point.
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var fetched struct {
		Data struct {
			ID        int64  `json:"id"`
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &fetched)
	t0 := fetched.Data.UpdatedAt
	if t0 == "" {
		t.Fatal("expected updated_at in fetched translation, got empty")
	}

	// --- Step 2: user A saves with the T0 expected_updated_at → should succeed.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          "first save — user A wins",
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// --- Step 3: user B (still holding T0) tries to save → should get 409.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          "second save — user B stale",
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for stale updated_at, got %d: %s", rec.Code, rec.Body.String())
	}

	// --- Step 4: verify user A's write survived (no silent overwrite by B).
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var after struct {
		Data struct {
			ActiveText *string `json:"active_text"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &after)
	if after.Data.ActiveText == nil || *after.Data.ActiveText != "first save — user A wins" {
		t.Errorf("expected user A's text preserved, got %v", after.Data.ActiveText)
	}
}

// TestIntegrationTranslationSave_FreshUpdatedAtSucceeds verifies the happy path:
// when the caller sends the current updated_at, the save goes through.
func TestIntegrationTranslationSave_FreshUpdatedAtSucceeds(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "freshput")
	projectID := createProject(t, router, "Fresh Update Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "fresh.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Source segment", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	// Two back-to-back saves, each using the updated_at returned by the previous one.
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var v1 struct {
		Data struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &v1)

	// Save #1 with the fresh updated_at
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          "hola",
			"editing_time_ms":     0,
			"expected_updated_at": v1.Data.UpdatedAt,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var savedV1 struct {
		Data struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &savedV1)

	// Save #2 using the NEW updated_at from save #1 — should still succeed.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          "hola mundo",
			"editing_time_ms":     0,
			"expected_updated_at": savedV1.Data.UpdatedAt,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationTranslationSave_NoExpectedUpdatedAtStillWorks verifies
// backwards compatibility: callers (old frontend, worker jobs, tests) that
// don't send expected_updated_at should still be able to save.
func TestIntegrationTranslationSave_NoExpectedUpdatedAtStillWorks(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "noexpected")
	projectID := createProject(t, router, "No Expected Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "noexpected.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Source", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":      "no expected version sent",
			"editing_time_ms": 0,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationSaveDraftDowngradesStatus pins the segment-lifecycle
// invariant: a draft save (auto-save +
// click-outside flush + Tab/F&R navigate transitions, all of which route
// through HandleSaveDraft) MUST set status='needs_review' for ANY starting
// status — including the human-confirmed ('approved') and QP-confirmed
// ('auto_approved') terminal states.
func TestIntegrationSaveDraftDowngradesStatus(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "draft_status_downgrade")
	projectID := createProject(t, router, "Draft Status Downgrade Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "draft_status.txt", db.FileFormatTxt)

	// Every value of translation_status enum. The test asserts that EVERY
	// starting state ends as 'needs_review' after a draft save — that's the
	// universal invariant the segment-lifecycle doc specifies. Including
	// 'needs_review' itself proves the no-op-stays-no-op case.
	startingStatuses := []db.TranslationStatus{
		db.TranslationStatusNeedsReview,
		db.TranslationStatusNeedsReview,
		db.TranslationStatusNeedsReview,
		db.TranslationStatusNeedsReview,
		db.TranslationStatusAutoApproved,
		db.TranslationStatusNeedsReview,
		db.TranslationStatusApproved,
	}

	for i, startStatus := range startingStatuses {
		t.Run(string(startStatus), func(t *testing.T) {
			seg := testutil.SeedSegment(t, q, file.ID, fmt.Sprintf("source for %s", startStatus), int32(i+1))
			tr := testutil.SeedTranslation(t, q, seg.ID, "es", startStatus)

			// Send a draft save through the actual HTTP handler — that's the path
			// auto-save + flushDraft + click-outside all go through. Asserting at
			// HTTP level (not direct SQL call) covers both the SQL and the handler
			// behavior, so a future handler-side regression that pre-validates
			// status would also be caught.
			rec := testutil.DoRequest(t, router, "PUT",
				fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
				map[string]any{
					"human_text":      "user typed something here",
					"editing_time_ms": 0,
				}, userID, tenantID)
			testutil.AssertStatus(t, rec, http.StatusOK)

			result, err := q.GetTranslation(ctx, tr.ID)
			if err != nil {
				t.Fatalf("get translation: %v", err)
			}
			if result.Status != db.TranslationStatusNeedsReview {
				t.Errorf("starting status=%s after SaveDraft should downgrade to %q, got %q. "+
					"Every starting status must end as needs_review.",
					startStatus, db.TranslationStatusNeedsReview, result.Status)
			}
			if result.ActiveText == nil || *result.ActiveText != "user typed something here" {
				t.Errorf("starting status=%s: expected human_text='user typed something here', got %v",
					startStatus, result.ActiveText)
			}

			// Losing the approval is a history event, and it is keyed on the
			// PRIOR status — so this table already walks both arms. Asserting
			// here is what makes the auto_approved arm real: it is the QE/APE
			// pipeline's normal output, and without this the arm runs but
			// nothing checks it, so dropping it from handler_history.go would
			// silently stop recording unapprovals on machine-approved segments.
			// The zero cases matter just as much: a row per draft save would
			// bury the panel while someone types.
			wantUnapprovals := 0
			if startStatus == db.TranslationStatusApproved || startStatus == db.TranslationStatusAutoApproved {
				wantUnapprovals = 1
			}
			rows, err := q.ListTranslationHistory(ctx, tr.ID)
			if err != nil {
				t.Fatalf("list history: %v", err)
			}
			got := 0
			for _, r := range rows {
				if r.Kind == db.TranslationHistoryKindUnconfirmed {
					got++
					if r.Text != nil {
						t.Errorf("starting status=%s: unconfirmed row carries text %q, want none — "+
							"text on a status row would become the next edit-distance baseline",
							startStatus, *r.Text)
					}
				}
			}
			if got != wantUnapprovals {
				t.Errorf("starting status=%s: %d unconfirmed history rows, want %d",
					startStatus, got, wantUnapprovals)
			}
		})
	}
}

// TestIntegrationConflict409BodyShape pins the 409 response body for the
// side-by-side conflict banner. The frontend
// renders "their version vs my edit" side-by-side from this payload — drift
// in the response shape silently breaks the banner.
//
// Body must contain:
//   - error.message — legacy plain-error key, kept for clients without banner support
//   - conflict.id, conflict.active_text, conflict.active_rich_text
//   - conflict.updated_at — ISO timestamp the client uses to refetch
//   - conflict.edited_by — user ID
//   - conflict.edited_by_name — user display name (rendered in the banner heading)
//
// Anti-tautology: each field is asserted to match the value that User A wrote,
// so a regression like "we returned the request body instead of the DB row"
// would surface immediately.
func TestIntegrationConflict409BodyShape(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "conflict-banner")
	projectID := createProject(t, router, "Conflict Banner Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "conflict.docx", db.FileFormatDocx)
	seg := testutil.SeedSegment(t, q, file.ID, "Conflict source", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var fetched struct {
		Data struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &fetched)
	t0 := fetched.Data.UpdatedAt

	// User A saves "their version" with the fresh updated_at.
	const theirVersion = "their saved version"
	const theirRich = "<g id=\"1\">their saved</g> version"
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          theirVersion,
			"human_rich_text":     theirRich,
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// User B (still on T0) tries to save → 409 with full body shape.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{
			"human_text":          "user B's stale draft",
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Conflict struct {
			ID             int64   `json:"id"`
			ActiveText     *string `json:"active_text"`
			ActiveRichText *string `json:"active_rich_text"`
			UpdatedAt      string  `json:"updated_at"`
			EditedBy       *int64  `json:"edited_by"`
			EditedByName   string  `json:"edited_by_name"`
		} `json:"conflict"`
	}
	testutil.DecodeJSON(t, rec, &body)

	if body.Error.Message == "" {
		t.Error("legacy `error.message` missing — older clients still rely on it")
	}
	if body.Conflict.ID != tr.ID {
		t.Errorf("conflict.id = %d, want %d", body.Conflict.ID, tr.ID)
	}
	// The conflict payload reports the visible target (active_text /
	// active_rich_text).
	if body.Conflict.ActiveRichText == nil || *body.Conflict.ActiveRichText != theirRich {
		t.Errorf("conflict.active_rich_text = %v, want %q", body.Conflict.ActiveRichText, theirRich)
	}
	if body.Conflict.ActiveText == nil || *body.Conflict.ActiveText != theirVersion {
		t.Errorf("conflict.active_text = %v, want %q", body.Conflict.ActiveText, theirVersion)
	}
	if body.Conflict.UpdatedAt == "" || body.Conflict.UpdatedAt == t0 {
		t.Errorf("conflict.updated_at must reflect the new server-side timestamp; got %q (was %q)", body.Conflict.UpdatedAt, t0)
	}
	if body.Conflict.EditedBy == nil || *body.Conflict.EditedBy != userID {
		t.Errorf("conflict.edited_by = %v, want %d", body.Conflict.EditedBy, userID)
	}
	if body.Conflict.EditedByName != "conflict-banner" {
		t.Errorf("conflict.edited_by_name = %q, want %q", body.Conflict.EditedByName, "conflict-banner")
	}
}

// TestIntegrationConflict409BodyShape_SaveDraftPath mirrors the above but for
// `PUT .../draft` (autosave). Same shape contract.
func TestIntegrationConflict409BodyShape_SaveDraftPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "conflict-draft")
	projectID := createProject(t, router, "Conflict Draft Project", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "draft-conflict.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Draft source", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		nil, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var fetched struct {
		Data struct {
			UpdatedAt string `json:"updated_at"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &fetched)
	t0 := fetched.Data.UpdatedAt

	// Save once with fresh updated_at via the draft path.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
		map[string]any{
			"human_text":          "their draft",
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Stale draft → 409 with conflict payload.
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", file.ID, tr.ID),
		map[string]any{
			"human_text":          "user B stale draft",
			"editing_time_ms":     0,
			"expected_updated_at": t0,
		},
		userID, tenantID,
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Conflict struct {
			ActiveText   *string `json:"active_text"`
			EditedByName string  `json:"edited_by_name"`
		} `json:"conflict"`
	}
	testutil.DecodeJSON(t, rec, &body)
	if body.Conflict.ActiveText == nil || *body.Conflict.ActiveText != "their draft" {
		t.Errorf("draft 409: conflict.active_text = %v, want %q", body.Conflict.ActiveText, "their draft")
	}
	if body.Conflict.EditedByName != "conflict-draft" {
		t.Errorf("draft 409: conflict.edited_by_name = %q, want %q", body.Conflict.EditedByName, "conflict-draft")
	}
}

// TestIntegrationGetTranslationConflictDetailsTenantScope pins the tenant
// boundary of the GetTranslationConflictDetails query directly. The handler already verifies tenant
// access via GetTranslationTenant before reaching the 409 branch, but every
// user-data query carries its own scope (CONTRIBUTING.md, "Tenant isolation")
// so a future caller can't accidentally bypass it.
// This test calls the query directly, NOT through the handler.
func TestIntegrationGetTranslationConflictDetailsTenantScope(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Tenant A owns the translation.
	tenantA := testutil.SeedTenant(t, q, "ConflictTenantA")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@conflict.com", "pass")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "ProjectA", "en", []string{"es"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "a.txt", db.FileFormatTxt)
	segA := testutil.SeedSegment(t, q, fileA.ID, "Hello", 1)
	trA := testutil.SeedTranslation(t, q, segA.ID, "es", db.TranslationStatusNeedsReview)

	// Mark trA as edited so the row has a meaningful edited_by.
	humanText := "Hola"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: trA.ID, HumanText: &humanText, EditedBy: &userA.ID,
	}); err != nil {
		t.Fatalf("seed update: %v", err)
	}

	// Tenant B exists but should NOT see tenant A's translation.
	tenantB := testutil.SeedTenant(t, q, "ConflictTenantB")

	// Same-tenant lookup succeeds.
	row, err := q.GetTranslationConflictDetails(ctx, db.GetTranslationConflictDetailsParams{
		ID: trA.ID, TenantID: tenantA.ID,
	})
	if err != nil {
		t.Fatalf("same-tenant lookup must succeed, got %v", err)
	}
	if row.ActiveText == nil || *row.ActiveText != "Hola" {
		t.Errorf("same-tenant: human_text = %v, want %q", row.ActiveText, "Hola")
	}

	// Cross-tenant lookup must return ErrNoRows (sqlc's :one returns no row).
	_, err = q.GetTranslationConflictDetails(ctx, db.GetTranslationConflictDetailsParams{
		ID: trA.ID, TenantID: tenantB.ID,
	})
	if err == nil {
		t.Fatal("cross-tenant lookup must fail; tenant boundary leaked")
	}
}

// =============================================================================
// Server-side QA blocking on confirm
// =============================================================================
//
// HandleUpdateHuman runs QA against the proposed target before applying the
// SQL update. If any rule fires with severity="error", the handler returns
// 422 with the issue list and the DB row is unchanged. Frontend-only
// blocking is bypassable via curl, so this gate is the load-bearing one.
//
// Drafts (HandleSaveDraft) are NOT gated — auto-save while typing must
// always succeed: QA only blocks approval, not draft saves.
//
// The rule loading mirrors the on-demand run-job path: same rule loader,
// same term-lookup logic, same consistency-check sibling
// resolution. So a translator who runs QA on the job and a translator who
// confirms a single segment see identical issue computation.
//
// =============================================================================
// Anti-tautology RED proofs.
// Each proof: (1) the exact source mutation, (2) the failing test + its
// diagnostic, (3) the date proven. Replay by reapplying the mutation and
// running `go test -count=1 -run <TestName> ./internal/translation/...`.
// =============================================================================
//
// Proof 1 — gate is load-bearing.
//   Mutation:    handler_qa.go::enforceQAOnConfirm — replace body with
//                `return false`. (Effectively: comment out the
//                CheckTranslationProposal call + the 422 emission.)
//   Test:        TestIntegration_QABlock_ConfirmRejectedOnError
//   Diagnostic:  "expected status 422, got 200 (body: {"data":...,"updated_tm_ids":[]})"
//                AND DB row mutates to "Hola sin placeholder" (bad data lands).
//   Proven:      2026-05-06.
//
// Proof 2 — severity policy is asymmetric (errors block, warnings don't).
//   Mutation:    qa/service.go::CheckResult.HasErrors — replace body with
//                `return false`.
//   Test:        TestIntegration_QABlock_ConfirmRejectedOnError fails (200);
//                TestIntegration_QABlock_ConfirmAllowedOnWarning still passes.
//   Diagnostic:  Error-block: "expected status 422, got 200".
//                Warning-allow: still GREEN (asymmetry verified).
//   Proven:      2026-05-06.
//
// Proof 3 — no-profile graceful path is the right escape hatch.
//   Mutation:    qa/service.go::CheckTranslationProposal — replace the
//                `if profile.QaProfileID == nil` branch with
//                `if profile.QaProfileID == nil || true` (always treat as
//                "no profile assigned").
//   Test:        TestIntegration_QABlock_NoProfileGracefullyAllows still
//                passes; TestIntegration_QABlock_ConfirmRejectedOnError
//                fails with 200 (gate skipped because "no profile" branch
//                taken erroneously).
//   Diagnostic:  Error-block: "expected status 422, got 200".
//   Proven:      2026-05-06. Catches the failure mode where the gate is
//                always skipped, regardless of whether a profile exists.
//
// The editor has no client-side pre-check: the server's 422 is the only gate,
// and web/src/components/SegmentRow.confirm-qa.test.tsx pins that the confirm
// request is always sent.

// setupQABlockProject creates a tenant + QA profile + project linked via
// settings_profile.qa_profile_id, plus a file + segment + translation
// the test can save against. The QA profile carries ONE rule
// (tag_checks) at the requested severity. Returns the IDs the test needs.
//
// Source uses `{name}` placeholder so the tag_checks rule fires when the
// target omits it. No rich text needed — `tagPattern` in checker.go also
// matches plain `{...}` placeholders.
func setupQABlockProject(t *testing.T, router http.Handler, q db.Querier, prefix, severity string, addRule bool) (userID, tenantID, fileID, trID int64) {
	t.Helper()
	userID, tenantID = registerUser(t, q, prefix)
	projectID := createProject(t, router, prefix+" Project", userID, tenantID)

	// Create QA profile.
	rec := testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name":        prefix + " QA",
		"description": "QA block fixture",
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var profileResp struct {
		Data struct{ ID int64 } `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profileResp)
	profileID := profileResp.Data.ID

	if addRule {
		rec = testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/qa-profiles/%d/rules", profileID),
			map[string]any{"check_type": "tag_checks", "enabled": true, "severity": severity},
			userID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	// Assign QA profile to the project's settings via PATCH.
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profileID},
		userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Seed file + segment with `{name}` placeholder + translation.
	file := testutil.SeedFile(t, q, projectID, "qa.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello {name}", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	return userID, tenantID, file.ID, tr.ID
}

// TestIntegration_QABlock_ConfirmRejectedOnError verifies the load-bearing
// gate: a confirm against text that triggers an error-severity QA rule
// returns 422 + issue list, and the DB row is unchanged. Anti-tautology:
// removing the qaChecker call in HandleUpdateHuman makes this test fail
// with 200, AND the DB row updates (the expensive bug we're protecting
// against — bad data lands).
func TestIntegration_QABlock_ConfirmRejectedOnError(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qablockerr", "error", true)

	// Save a translation that DROPS the {name} placeholder — tag_checks fires.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola sin placeholder", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusUnprocessableEntity)

	// Body must carry the QA issues so the FE can render them.
	var resp struct {
		Error    map[string]any `json:"error"`
		QaIssues []struct {
			CheckType string `json:"check_type"`
			Severity  string `json:"severity"`
			Message   string `json:"message"`
		} `json:"qa_issues"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.QaIssues) == 0 {
		t.Fatal("422 response carries no qa_issues — frontend would have to render generic error toast")
	}
	// `tag_checks` is the rule key; the runtime emits more specific check
	// types like `tag_missing` / `tag_extra` / `tag_unclosed`. Any of
	// these counts as evidence the rule fired with severity=error.
	foundTagError := false
	for _, iss := range resp.QaIssues {
		if strings.HasPrefix(iss.CheckType, "tag_") && iss.Severity == "error" {
			foundTagError = true
			break
		}
	}
	if !foundTagError {
		t.Errorf("expected at least one tag_*/error issue, got %+v", resp.QaIssues)
	}

	// And the DB row is unchanged — the SQL update never ran. This is the
	// load-bearing observable behavior: 422 alone could be a lie if the
	// handler still mutated. Audit fix N1: explicit `nil` assertion plus
	// the proposed-text negative — together they pin "row was not
	// mutated to ANY value, especially not the bad one."
	got, err := q.GetTranslation(context.Background(), trID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText != nil {
		t.Errorf("human_text should be nil after rejected save, got %q", *got.ActiveText)
	}
	if got.Status == db.TranslationStatusApproved {
		t.Error("status was promoted to approved despite 422 — handler did not short-circuit before the UPDATE")
	}
}

// TestIntegration_QABlock_ConfirmAllowedOnWarning verifies the
// asymmetric severity policy: only error blocks. A QA rule firing at
// "warning" must NOT 422 the request — translators see warnings as
// guidance, not as a wall.
func TestIntegration_QABlock_ConfirmAllowedOnWarning(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qablockwarn", "warning", true)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola sin placeholder", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	got, err := q.GetTranslation(context.Background(), trID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText == nil || *got.ActiveText != "Hola sin placeholder" {
		t.Errorf("expected human_text='Hola sin placeholder', got %v — warning-severity should NOT block", got.ActiveText)
	}
}

// TestIntegration_QABlock_NoProfileGracefullyAllows verifies graceful
// degradation. A project without a QA profile assigned skips enforcement
// entirely. Otherwise translators on a project with an admin who hasn't
// configured QA yet would be trapped behind an undefined gate.
func TestIntegration_QABlock_NoProfileGracefullyAllows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "qablocknoprof")
	projectID := createProject(t, router, "No-Profile Project", userID, tenantID)

	// Skip the profile creation/assignment — settings_profile.qa_profile_id is NULL.
	file := testutil.SeedFile(t, q, projectID, "noprof.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello {name}", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Anything goes here", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegration_QABlock_DraftSaveBypassesQA verifies the draft path is
// untouched. Auto-save while typing must always succeed — even mid-edit
// with errors visible. The 6a gate is on `HandleUpdateHuman` (confirm),
// not `HandleSaveDraft`.
func TestIntegration_QABlock_DraftSaveBypassesQA(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qablockdraft", "error", true)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", fileID, trID),
		map[string]any{"human_text": "Hola sin placeholder", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	got, err := q.GetTranslation(context.Background(), trID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText == nil || *got.ActiveText != "Hola sin placeholder" {
		t.Errorf("draft did not save: got %v", got.ActiveText)
	}
}

// TestIntegration_QABlock_StaleResultsAreIgnored verifies that the gate
// re-runs QA against the PROPOSED text rather than reading stored
// `qa_results`. Pre-existing stale rows could otherwise either falsely
// block a clean save (false positive) or falsely allow a bad save
// (false negative). Both would defeat the purpose of save-time
// enforcement.
//
// Setup: project with the {name}-tag rule + a STALE qa_result row for
// the translation (saying tag mismatch). Then PUT a CLEAN target that
// includes {name}. Server re-runs QA, sees no issues, accepts. If the
// gate read the stale row instead, it'd 422 a valid save.
func TestIntegration_QABlock_StaleResultsAreIgnored(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qablockstale", "error", true)

	// Plant a stale QA result on this translation — direct DB write so
	// the test bypasses the on-demand run path.
	if err := q.UpsertQAResult(context.Background(), db.UpsertQAResultParams{
		TranslationID: trID,
		CheckType:     "tag_checks",
		Severity:      "error",
		Message:       "STALE — should be ignored by save-time check",
	}); err != nil {
		t.Fatalf("plant stale qa_result: %v", err)
	}

	// Save a CLEAN target (preserves the {name} placeholder).
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola {name}", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegration_QABlock_ProfileWithNoRulesAllows verifies the third
// graceful-degradation path: profile assigned, but no rules enabled
// (or all rules disabled). The gate must not 422 in that case — there's
// nothing to enforce. Mirrors the on-demand run path's "no enabled
// rules → 0 issues" early return.
func TestIntegration_QABlock_ProfileWithNoRulesAllows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	// addRule=false → profile exists, but has no rules.
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qablocknorules", "error", false)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola sin placeholder", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegration_QABlock_CrossTenantReturns404 pins the cross-tenant
// gate on the QA-augmented confirm path. Tenant B sends its own file's URL —
// which the file-ownership middleware lets through — carrying tenant A's
// translation id, so the confirm's tenant- and file-scoped lookup is what must
// answer 404 before the QA service sees the request.
//
// `qa.Service.CheckTranslationProposal` also looks the translation up by
// tenant, so without the confirm's own guard the request would fail later
// with a 500. The test asserts 404, not "any non-200".
func TestIntegration_QABlock_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Tenant A has the translation + QA profile. We don't use userA/tenantA
	// after setup — the attacker is tenant B, below.
	_, _, _, trA := setupQABlockProject(t, router, q, "qaxa", "error", true)

	// Tenant B is a separate registration with its own file. They try to
	// confirm tenant A's translation by ID through that file — must 404.
	userB, tenantB := registerUser(t, q, "qaxb")
	projectB := createProject(t, router, "qaxb Project", userB, tenantB)
	fileB := testutil.SeedFile(t, q, projectB, "b.txt", db.FileFormatTxt)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileB.ID, trA),
		map[string]any{"human_text": "Hola {name}", "editing_time_ms": 0},
		userB, tenantB,
	)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// And the row in tenant A is untouched — even with clean text that
	// would have passed QA, cross-tenant access must NOT mutate.
	got, err := q.GetTranslation(context.Background(), trA)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText != nil {
		t.Errorf("cross-tenant write leaked through: human_text=%q", *got.ActiveText)
	}
}

// TestIntegration_QABlock_UseMineRetryRunsQA pins the contract that the
// "use mine" conflict-resolution path (re-issue with
// `expected_updated_at=null` to bypass the optimistic-lock check) STILL
// runs the QA gate. Without this guarantee, a user who hit a 409
// could escape QA enforcement by clicking "use mine" with bad text.
//
// Setup: standard QA-blocked project + a translation. PUT bad text
// with NO expected_updated_at → expect 422 (same as the main path).
// Audit fix I5.
func TestIntegration_QABlock_UseMineRetryRunsQA(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID, fileID, trID := setupQABlockProject(t, router, q, "qausemine", "error", true)

	// Mimic the FE's "use mine" call: PUT without expected_updated_at,
	// the server is supposed to skip the optimistic-lock check but
	// still run QA.
	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola sin placeholder", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusUnprocessableEntity)

	// Row is untouched.
	got, err := q.GetTranslation(context.Background(), trID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText != nil {
		t.Errorf("use-mine save leaked through despite QA error: human_text=%q", *got.ActiveText)
	}

	// Same path with CLEAN text → 200 (sibling case for I5).
	rec = testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", fileID, trID),
		map[string]any{"human_text": "Hola {name}", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// =============================================================================
// HandleUpdateHuman returns updated_tm_ids[] for FE cache invalidation
// =============================================================================
//
// HandleUpdateHuman calls SaveToTM after committing the human edit. The
// response includes the IDs of TMs that received the entry so the
// frontend can invalidate exactly the right TM browser caches without a
// blanket invalidate (which would refetch every open TM list).
//
// The contract: `updated_tm_ids` is ALWAYS present in the response (empty
// array if nothing was written). Frontend can iterate without null checks.

// TestIntegrationUpdateHuman_ResponseEchoesUpdatedTMIDs configures a project
// with a write TM, saves a confirmed translation via PUT, and asserts the
// response body contains the assignment's TM ID. Anti-tautology RED proven:
// reverting SaveToTM to discard the IDs (return nil) makes this test fail.
func TestIntegrationUpdateHuman_ResponseEchoesUpdatedTMIDs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "tmwriteback")
	projectID := createProject(t, router, "TM Writeback Project", userID, tenantID)

	// Configure a write TM on the project's settings profile so SaveToTM
	// has somewhere to write. Without this assignment, updated_tm_ids is
	// the empty array — we want a positive case here.
	tm := testutil.SeedTM(t, q, tenantID, "Write TM")
	proj, err := q.GetProjectByID(ctx, projectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: proj.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	file := testutil.SeedFile(t, q, projectID, "tm.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Hola", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data         map[string]any `json:"data"`
		UpdatedTMIDs []int64        `json:"updated_tm_ids"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	if len(resp.UpdatedTMIDs) != 1 {
		t.Fatalf("expected exactly 1 TM ID in response, got %d (ids=%v)", len(resp.UpdatedTMIDs), resp.UpdatedTMIDs)
	}
	if resp.UpdatedTMIDs[0] != tm.ID {
		t.Errorf("expected updated_tm_ids[0]=%d, got %d", tm.ID, resp.UpdatedTMIDs[0])
	}
	// And the entry actually landed in the TM (otherwise the response would
	// be a lie — the IDs say "written" but the row isn't there).
	entry, err := q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tm.ID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTM: %v", err)
	}
	if entry.TargetText != "Hola" {
		t.Errorf("TM entry target=%q, want %q", entry.TargetText, "Hola")
	}
}

// TestIntegrationUpdateHuman_ResponseHasEmptyArrayWhenNoWriteTMs verifies
// the contract when no TMs are configured: the field is present, just
// empty. The frontend's `for (const id of updatedTmIds)` loop becomes a
// no-op (which is correct — nothing to invalidate). Anti-tautology RED:
// changing the handler to omit the field on the empty path makes this test
// fail (json decode would leave UpdatedTMIDs as nil — distinct from []).
func TestIntegrationUpdateHuman_ResponseHasEmptyArrayWhenNoWriteTMs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	userID, tenantID := registerUser(t, q, "noTMwrite")
	projectID := createProject(t, router, "No TM Project", userID, tenantID)
	// Deliberately NO CreateSettingsTMAssignment — SaveToTM will return
	// nil tm IDs, but the handler must still emit `[]` not `null`.

	file := testutil.SeedFile(t, q, projectID, "no.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)

	rec := testutil.DoRequest(t, router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
		map[string]any{"human_text": "Hola", "editing_time_ms": 0},
		userID, tenantID,
	)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Decode raw JSON so we can distinguish "null" from "[]" — Go's nil
	// slice decodes from "null" but a JSON empty array decodes to []int64{}
	// (length 0, not nil). The contract is "always present, never null."
	var raw map[string]json.RawMessage
	testutil.DecodeJSON(t, rec, &raw)
	updatedRaw, ok := raw["updated_tm_ids"]
	if !ok {
		t.Fatal("response missing updated_tm_ids field — frontend would have to null-check")
	}
	if string(updatedRaw) != "[]" {
		t.Errorf("expected updated_tm_ids=[], got %s", string(updatedRaw))
	}
}

// =============================================================================
// Segment lock/unlock authorization
// =============================================================================
//
// Lock is open to every member, unlock is owner/admin only (so a member
// cannot undo a PM's protective lock), and both are tenant-scoped (IDs are
// guessable, so every query touching user data carries tenant_id).
//
// These tests pin:
//   - Cross-tenant returns 404 (tenant boundary holds even with admin role)
//   - Member-in-same-tenant gets 403 on unlock (role gate)
//   - Member can still lock (asymmetric policy: flagging is open, undoing
//     the flag requires authority)
//   - Admin can unlock (happy path)
//
// Anti-tautology RED proofs (run manually):
//   - Remove the GetTranslationTenant guard in HandleUnlock → cross-tenant
//     test must fail (200 instead of 404).
//   - Remove RequireRole on POST /unlock route → member-403 test must fail.

// TestIntegrationUnlockAdminAllowed verifies the happy path: admin role
// reaches the handler, tenant scope passes, DB row flips to unlocked.
func TestIntegrationUnlockAdminAllowed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "unlockadmin")
	projectID := createProject(t, router, "Unlock Admin", userID, tenantID)

	file := testutil.SeedFile(t, q, projectID, "f.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	if err := q.LockTranslation(context.Background(), tr.ID); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/unlock", file.ID, tr.ID),
		nil, userID, tenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	got, err := q.GetTranslation(context.Background(), tr.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.Locked {
		t.Fatal("translation still locked after admin unlock; the SQL did not run")
	}
}

// TestIntegrationLockUnlockCrossTenant404 pins the tenant guard inside
// HandleLock/HandleUnlock. Tenant B sends its own file's URL — which the
// file-ownership middleware lets through — carrying tenant A's translation
// id, so only the handler's tenant- and file-scoped lookup stands between
// tenant B and the lock/unlock SQL, which takes a bare id. Dropping that
// lookup from either handler turns the 404 into a 200.
func TestIntegrationLockUnlockCrossTenant404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Tenant A owns the translation.
	userA, tenantA := registerUser(t, q, "tenant-a")
	projectA := createProject(t, router, "Project A", userA, tenantA)
	fileA := testutil.SeedFile(t, q, projectA, "a.txt", db.FileFormatTxt)
	segA := testutil.SeedSegment(t, q, fileA.ID, "Hello A", 1)
	trA := testutil.SeedTranslation(t, q, segA.ID, "es", db.TranslationStatusNeedsReview)
	if err := q.LockTranslation(context.Background(), trA.ID); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	// Tenant B has its own admin and its own file, and aims at trA through it.
	userB, tenantB := registerUser(t, q, "tenant-b")
	projectB := createProject(t, router, "Project B", userB, tenantB)
	fileB := testutil.SeedFile(t, q, projectB, "b.txt", db.FileFormatTxt)

	// Cross-tenant unlock → 404. trA exists, but not in tenant B's scope.
	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/unlock", fileB.ID, trA.ID),
		nil, userB, tenantB, "admin")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// DB row must still be locked — handler must not have reached the SQL.
	got, err := q.GetTranslation(context.Background(), trA.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if !got.Locked {
		t.Fatal("cross-tenant unlock leaked through; trA was unlocked by tenant B admin")
	}

	// Cross-tenant lock attempt — same tenant boundary, different mutation.
	// Use a fresh translation in tenant A to make the mutation observable.
	segA2 := testutil.SeedSegment(t, q, fileA.ID, "Hello A2", 2)
	trA2 := testutil.SeedTranslation(t, q, segA2.ID, "es", db.TranslationStatusNeedsReview)

	rec = testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/lock", fileB.ID, trA2.ID),
		nil, userB, tenantB, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	got2, err := q.GetTranslation(context.Background(), trA2.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got2.Locked {
		t.Fatal("cross-tenant lock leaked through; trA2 was locked by tenant B owner")
	}
}

// TestIntegration_HandlePropagate_CrossTenantReturns404 pins the tenant boundary
// on POST /api/files/{fileID}/propagate: fileID is sequential, so
// without the GetFileTenant guard any authenticated user could overwrite
// another tenant's translations en masse.
//
// Anti-tautology: removing the GetFileTenant guard makes this test fail with
// 200 + {propagated: N} (the dangerous response shape) instead of 404.
func TestIntegration_HandlePropagate_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	// Tenant A — owns the file and a confirmed translation. Two segments
	// share the same source text so propagation has a real target.
	userA, tenantA := registerUser(t, q, "propxa")
	projectA := createProject(t, router, "Prop A", userA, tenantA)
	fileA := testutil.SeedFile(t, q, projectA, "a.txt", db.FileFormatTxt)
	segA1 := testutil.SeedSegment(t, q, fileA.ID, "Hello world", 1)
	segA2 := testutil.SeedSegment(t, q, fileA.ID, "Hello world", 2)
	trA1 := testutil.SeedTranslation(t, q, segA1.ID, "es", db.TranslationStatusApproved)
	trA2 := testutil.SeedTranslation(t, q, segA2.ID, "es", db.TranslationStatusNeedsReview)

	// Tenant B — attacker. Registered separately, no shared resources.
	userB, tenantB := registerUser(t, q, "propxb")

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/files/%d/propagate", fileA.ID),
		map[string]any{
			"source_text":     "Hello world",
			"target_language": "es",
			"human_text":      "ATTACKER OVERWRITE",
			"exclude_id":      trA1.ID, // mimic legit-shaped request
		},
		userB, tenantB,
	)
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Verify tenant A's row is untouched — even with cross-tenant input that
	// matched the source-text key, the propagation must NOT have run.
	got, err := q.GetTranslation(context.Background(), trA2.ID)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.ActiveText != nil {
		t.Errorf("cross-tenant propagation leaked through: human_text=%q", *got.ActiveText)
	}
}

// Propagation overwrites a sibling's target, so the sibling's stored QA
// findings — and any ignore on them — describe text that is gone. Confirm
// re-runs QA only for the segment being confirmed, so nothing else would
// correct them.
//
// Anti-tautology RED proof: drop the DeleteQAResultsForTranslations call from
// HandlePropagate and the sibling assertions below fail while the excluded
// row's assertion still passes.
func TestIntegrationPropagate_DropsSiblingQAFindings(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	userID, tenantID := registerUser(t, q, "propagate-qa")
	projectID := createProject(t, router, "Propagate QA Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, "propagate-qa.txt", db.FileFormatTxt)

	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Hello world", 2)
	// Different source — never propagated onto, so its findings must survive.
	seg3 := testutil.SeedSegment(t, q, file.ID, "Goodbye world", 3)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusNeedsReview)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusNeedsReview)
	tr3 := testutil.SeedTranslation(t, q, seg3.ID, "es", db.TranslationStatusNeedsReview)

	humanText := "Hola mundo"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: tr1.ID, HumanText: &humanText,
	}); err != nil {
		t.Fatalf("update translation human: %v", err)
	}

	seedQAResult := func(id int64, checkType string) {
		t.Helper()
		if err := q.UpsertQAResult(ctx, db.UpsertQAResultParams{
			TranslationID: id, CheckType: checkType, Severity: "error", Message: "stale finding",
		}); err != nil {
			t.Fatalf("seed qa_result(%d): %v", id, err)
		}
	}
	countQA := func(id int64) int {
		t.Helper()
		rows, err := q.ListQAResultsForTranslation(ctx, id)
		if err != nil {
			t.Fatalf("list qa_results(%d): %v", id, err)
		}
		return len(rows)
	}
	seedQAResult(tr1.ID, "punctuation")
	seedQAResult(tr2.ID, "punctuation")
	seedQAResult(tr3.ID, "punctuation")

	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/files/%d/propagate", file.ID), map[string]any{
		"source_text":     "Hello world",
		"target_language": "es",
		"human_text":      humanText,
		"exclude_id":      tr1.ID,
	}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if n := countQA(tr2.ID); n != 0 {
		t.Errorf("propagated-onto sibling must lose its findings, got %d", n)
	}
	// tr1 is excluded from the propagation — its own text did not change here,
	// and the confirm path re-runs QA for it separately.
	if n := countQA(tr1.ID); n != 1 {
		t.Errorf("excluded source row must keep its findings, got %d", n)
	}
	if n := countQA(tr3.ID); n != 1 {
		t.Errorf("unrelated segment must keep its findings, got %d", n)
	}
}
