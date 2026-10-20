package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// In a JSON, PO, TXT or XLSX file a translation may hold only the tags its
// source has: confirming one with another tag is refused as a QA error, one
// by one and in bulk, whether or not the project has a QA profile. Other
// formats escape typed tags on export and are not refused.
func TestIntegrationTranslation_ConfirmRefusesTagNotInSource(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	user, tenantID := testutil.RegisterUser(t, q, "src-tags")
	project := testutil.SeedProject(t, q, tenantID, "Source tags", "en", []string{"de"})
	const sourceRich = `Click <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;https://example.com/&quot;&gt;">here</g> now`
	const keptLink = `Klick <g id="1" ctype="x-html-a" original_markup="&lt;a href=&quot;https://example.com/&quot;&gt;">hier</g> jetzt`

	seed := func(format db.FileFormat) (db.Job, db.Translation) {
		t.Helper()
		f := testutil.SeedFile(t, q, project.ID, "f."+string(format), format)
		seg := testutil.SeedSegment(t, q, f.ID, "Click here now", 1)
		if _, err := pool.Exec(ctx, "UPDATE segments SET source_rich_text = $2 WHERE id = $1", seg.ID, sourceRich); err != nil {
			t.Fatalf("set source rich text: %v", err)
		}
		return testutil.SeedJob(t, q, f.ID, "de"), testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	}
	confirm := func(fileID, id int64, text string, rich *string) (int, string) {
		body := map[string]any{"human_text": text}
		if rich != nil {
			body["human_rich_text"] = *rich
		}
		rec := testutil.DoRequest(t, router, http.MethodPut, fmt.Sprintf("/api/files/%d/translations/%d", fileID, id), body, user, tenantID)
		return rec.Code, rec.Body.String()
	}

	for _, format := range []db.FileFormat{db.FileFormatTxt, db.FileFormatJsonI18n, db.FileFormatPo, db.FileFormatPot, db.FileFormatXlsx} {
		t.Run(string(format), func(t *testing.T) {
			job, tr := seed(format)
			code, body := confirm(job.FileID, tr.ID, "Klick hier <img src=x onerror=alert(1)>", nil)
			if code != http.StatusUnprocessableEntity || !strings.Contains(body, `"tag_not_in_source"`) || !strings.Contains(body, "img src=x onerror=alert(1)") {
				t.Errorf("confirm with a foreign tag: want 422 naming the tag, got %d: %s", code, body)
			}
			// A chip that is not converted back is written as text: refused.
			stray := keptLink + `<x id="5" ctype="x-other"/>`
			if code, body := confirm(job.FileID, tr.ID, "Klick hier jetzt", &stray); code != http.StatusUnprocessableEntity {
				t.Errorf("confirm with a chip the export cannot convert: want 422, got %d: %s", code, body)
			}
			// A chip's own attributes never reach the export (it takes the
			// source's), so a differing href is not refused.
			forged := strings.Replace(keptLink, "https://example.com/", "javascript:alert(1)", 1)
			if code, body := confirm(job.FileID, tr.ID, "Klick hier jetzt", &forged); code != http.StatusOK {
				t.Errorf("confirm with a chip whose own href differs: want 200, got %d: %s", code, body)
			}
			link := keptLink
			if code, body := confirm(job.FileID, tr.ID, "Klick hier jetzt", &link); code != http.StatusOK {
				t.Errorf("confirm keeping the source's link: want 200, got %d: %s", code, body)
			}
			// Shift+Enter in a segment with chips saves a line-break chip.
			lineBreak := strings.Replace(keptLink, " jetzt", `<x id="2" ctype="lb" original_tag="br"/>jetzt`, 1)
			if code, body := confirm(job.FileID, tr.ID, "Klick hier\njetzt", &lineBreak); code != http.StatusOK {
				t.Errorf("confirm with a line break: want 200, got %d: %s", code, body)
			}
		})
	}

	t.Run("bulk", func(t *testing.T) {
		job, tr := seed(db.FileFormatTxt)
		if _, err := pool.Exec(ctx, "UPDATE translations SET active_text = $2 WHERE id = $1", tr.ID, "Klick <b>hier</b>"); err != nil {
			t.Fatalf("set draft: %v", err)
		}
		rec := testutil.DoRequest(t, router, http.MethodPost, fmt.Sprintf("/api/jobs/%d/translations/confirm", job.ID),
			map[string]any{"translation_ids": []int64{tr.ID}}, user, tenantID)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tag_not_in_source"`) {
			t.Errorf("bulk confirm: want the row skipped as tag_not_in_source, got %d: %s", rec.Code, rec.Body.String())
		}
		after, err := q.GetTranslation(ctx, tr.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if after.Status == db.TranslationStatusApproved {
			t.Error("bulk confirm approved a row with a tag the source does not have")
		}
	})

	t.Run("plain-text strings are not refused", func(t *testing.T) {
		job, tr := seed(db.FileFormatJsonI18n)
		if _, err := pool.Exec(ctx, "UPDATE files SET inline_html = false WHERE id = $1", job.FileID); err != nil {
			t.Fatalf("set plain text: %v", err)
		}
		if code, body := confirm(job.FileID, tr.ID, "Nutze <b> und <img src=x>", nil); code != http.StatusOK {
			t.Errorf("plain-text confirm: want 200, got %d: %s", code, body)
		}
	})

	t.Run("docx is not refused", func(t *testing.T) {
		job, tr := seed(db.FileFormatDocx)
		if code, body := confirm(job.FileID, tr.ID, "Klick hier <b>", nil); code != http.StatusOK {
			t.Errorf("docx confirm: want 200, got %d: %s", code, body)
		}
	})
}
