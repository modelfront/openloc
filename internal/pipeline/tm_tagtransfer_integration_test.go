package pipeline_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/qa"
	"openloc/internal/testutil"
)

// What a file parser stores for a tagged segment: XLIFF <g> with an id.
const richSourceG = `Fill the tank with H<g id="1" ctype="x-html-sub">2</g>O before the first run.`

// TMX markup in the shape an SDLX export produces, as it lands in a TM entry's
// target_rich_text. TMX 1.4 pairs the halves of an inline tag with `i`, NOT
// `id`; keep that exact tag shape — a simplified one would not exercise the remap.
const richTargetTMX = `Remplissez le réservoir avec H<bpt i="1" x="1">&lt;1&gt;</bpt>2<ept i="1">&lt;/1&gt;</ept>O avant la première utilisation.`

// TestIntegrationTMPhase_ExactMatchCarriesTagsToTarget pins that a 100% TM match
// on a tagged segment writes the source's tags into active_rich_text, so neither
// the missing-tags bar nor the tag QA check fires on it (TM path).
func TestIntegrationTMPhase_ExactMatchCarriesTagsToTarget(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	const plainSource = "Fill the tank with H2O before the first run."
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{plainSource})

	// Give the segment the tagged source form the parser would have stored.
	if _, err := pool.Exec(ctx,
		`UPDATE segments SET source_rich_text = $1 WHERE file_id = $2`,
		richSourceG, fileID); err != nil {
		t.Fatalf("set source_rich_text: %v", err)
	}
	// Assert the precondition landed — otherwise a seeding change could make
	// this test pass by never exercising a tagged segment at all.
	var seededRich *string
	if err := pool.QueryRow(ctx,
		`SELECT source_rich_text FROM segments WHERE file_id = $1`, fileID).Scan(&seededRich); err != nil {
		t.Fatalf("read back source_rich_text: %v", err)
	}
	if seededRich == nil || *seededRich != richSourceG {
		t.Fatalf("precondition: segment has no tagged source, got %v", seededRich)
	}

	// A TM entry whose target is plain text — the common case for TMs built
	// from another tool, and the one that produced the report.
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     plainSource,
		TargetText:     "Remplissez le réservoir avec H2O avant la première utilisation.",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil,
		pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID: fileID, TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 translation, got %d", len(rows))
	}
	tr := rows[0]

	// Precondition: it really was a 100% match.
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Fatalf("precondition: want a 100%% match, got %v", tr.TmMatchPercent)
	}

	// 1) The target must carry the source's tags.
	if tr.ActiveRichText == nil || *tr.ActiveRichText == "" {
		t.Fatalf("active_rich_text is empty — the TM match dropped every tag from a tagged segment")
	}
	// Industry practice: the ids come from the CURRENT source segment, never
	// from the TM entry.
	if !strings.Contains(*tr.ActiveRichText, `<g id="1" ctype="x-html-sub">`) {
		t.Errorf("target should carry the source's own tag, got: %q", *tr.ActiveRichText)
	}

	// 2) The user-visible consequence: tag QA must be clean. This is the
	//    assertion that maps to "even after 100% tm match there is a warning".
	issues := qa.CheckTagsDetailed(richSourceG, *tr.ActiveRichText, "error", qa.DefaultTagChecksConfig())
	if len(issues) != 0 {
		for _, is := range issues {
			t.Errorf("unexpected tag QA issue on a 100%% match: %+v", is)
		}
	}
}

// TestIntegrationTMPhase_UntaggedSourceStaysPlain proves the fix is scoped:
// segments with no tags must not gain a rich-text form, which would churn every
// ordinary segment in the database.
func TestIntegrationTMPhase_UntaggedSourceStaysPlain(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil,
		pipeline.TranslatePhases{UseTM: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID: fileID, TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	tr := rows[0]

	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Errorf("active_text: got %v", tr.ActiveText)
	}
	if tr.ActiveRichText != nil && *tr.ActiveRichText != "" {
		t.Errorf("tag-free segment should not gain rich text, got %q", *tr.ActiveRichText)
	}
}
