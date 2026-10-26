package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parsers/xliff"
	"openloc/internal/pipeline"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// The bilingual export carries each segment's pre-translation: the XLIFF
// state-qualifier, the TM score (openloc:match-percent + alt-trans
// match-quality) and the matched TM unit as <alt-trans>.
func TestIntegration_BilingualExport_PreTranslationMatch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := setupBilingualExportFixture(t, q, "match", "en", []string{"es"})
	liveTM := testutil.SeedTM(t, q, fx.TenantID, "Live TM")
	deletedTM := testutil.SeedTM(t, q, fx.TenantID, "Deleted TM")
	otherTenant := testutil.SeedTenant(t, q, "Other Corp")
	foreignTM := testutil.SeedTM(t, q, otherTenant.ID, "Foreign TM")

	tmUnit := func(tmID int64, src, tgt string) db.TranslationMemory {
		t.Helper()
		e, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tmID, SourceLanguage: "en", TargetLanguage: "es",
			SourceText: src, TargetText: tgt, CreatedBy: "test",
		})
		if err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
		return e
	}
	// seed makes a needs_review segment pre-translated by `kind`, then sets
	// its visible target to `active` (a later human edit when it differs).
	seed := func(pos int32, key, source, active string, kind db.TranslationHistoryKind, origin db.TranslationOrigin, percent *int16, unit *db.TranslationMemory) {
		t.Helper()
		seg := seedSegmentWithKey(t, q, fx.FileID, key, source, pos)
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
		opts := pipeline.AutomationEmitOpts{TranslationID: tr.ID, Kind: kind, Origin: origin, Text: &active, TmMatchPercent: percent}
		if unit != nil {
			opts.TmID, opts.TmEntryID = &unit.TmID, &unit.ID
		}
		if err := pipeline.EmitAutomationHistoryInTx(ctx, q, opts); err != nil {
			t.Fatalf("emit %s history: %v", key, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = $1 WHERE id = $2`, active, tr.ID); err != nil {
			t.Fatalf("set target %s: %v", key, err)
		}
	}
	pct := ptr.To[int16]

	live := tmUnit(liveTM.ID, "Double tap the left earbud.", "Toca dos veces el auricular izquierdo.")
	gone := tmUnit(deletedTM.ID, "Gone source", "Gone target")
	foreign := tmUnit(foreignTM.ID, "Foreign source", "Foreign target")

	seed(1, "line:1", "Triple tap the right earbud.", "Toca tres veces el auricular derecho.", db.TranslationHistoryKindTm, db.TranslationOriginTm, pct(74), &live)
	seed(2, "line:2", "Deleted TM segment", "Destino", db.TranslationHistoryKindTm, db.TranslationOriginTm, pct(92), &gone)
	seed(3, "line:3", "Foreign TM segment", "Destino ajeno", db.TranslationHistoryKindTm, db.TranslationOriginTm, pct(80), &foreign)
	seed(4, "line:4", "Machine translated", "Traducido por máquina", db.TranslationHistoryKindMt, db.TranslationOriginMt, nil, nil)

	if n, err := q.SoftDeleteTMCollection(ctx, db.SoftDeleteTMCollectionParams{ID: deletedTM.ID, TenantID: fx.TenantID}); err != nil || n != 1 {
		t.Fatalf("SoftDeleteTMCollection: n=%d err=%v", n, err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	data, _, err := svc.ExportBilingualXLIFF(ctx, fx.TenantID, fx.FileID, "en", "es")
	if err != nil {
		t.Fatalf("ExportBilingualXLIFF: %v", err)
	}
	body := string(data)
	unit := func(key string) string {
		start := strings.Index(body, fmt.Sprintf(`<trans-unit id="%s"`, key))
		if start < 0 {
			t.Fatalf("trans-unit %s missing:\n%s", key, body)
		}
		end := strings.Index(body[start:], "</trans-unit>")
		return body[start : start+end]
	}

	// TM fuzzy match whose TM unit still exists: everything is written, and
	// the alt-trans holds the TM unit, not the segment's own (edited) text.
	u1 := unit("line:1")
	for _, want := range []string{
		`<trans-unit id="line:1" openloc:match-percent="74">`,
		`<target state="needs-review-translation" state-qualifier="fuzzy-match">Toca tres veces el auricular derecho.</target>`,
		`<alt-trans match-quality="74" origin="tm">`,
		`<source>Double tap the left earbud.</source>`,
		`<target>Toca dos veces el auricular izquierdo.</target>`,
	} {
		if !strings.Contains(u1, want) {
			t.Errorf("line:1 missing %s\n%s", want, u1)
		}
	}

	// The TM was deleted, or belongs to another tenant: the score stays,
	// the TM unit's text is never exported.
	for _, c := range []struct{ key, percent, leaked string }{
		{"line:2", "92", "Gone source"},
		{"line:3", "80", "Foreign source"},
	} {
		u := unit(c.key)
		if !strings.Contains(u, `openloc:match-percent="`+c.percent+`"`) || !strings.Contains(u, `state-qualifier="fuzzy-match"`) {
			t.Errorf("%s should keep its score and qualifier:\n%s", c.key, u)
		}
		if strings.Contains(u, "<alt-trans") || strings.Contains(body, c.leaked) {
			t.Errorf("%s must not export the TM unit:\n%s", c.key, u)
		}
	}

	// MT pre-translation: qualifier only.
	u4 := unit("line:4")
	if !strings.Contains(u4, `state-qualifier="leveraged-mt"`) || strings.Contains(u4, "match-percent") || strings.Contains(u4, "<alt-trans") {
		t.Errorf("line:4 should carry only leveraged-mt:\n%s", u4)
	}

	// The file still parses to the segments' own source and target.
	segs, err := xliff.XLIFF{}.Parse(data)
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if len(segs) != 4 || segs[0].Text != "Triple tap the right earbud." || segs[0].Target != "Toca tres veces el auricular derecho." {
		t.Errorf("re-parse picked up alt-trans content: %+v", segs)
	}
}
