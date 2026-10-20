package file_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// A bilingual file brings translations for its own target language only. A
// German XLIFF in a German + French project fills German; French stays
// untranslated. A file whose language is not one of the project's, or that
// names none in a project with several targets, imports no translations and
// the upload says so. With one target and no language named, that target gets
// them.
func TestIntegrationUploadBilingualFillsOnlyItsOwnLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "BilingualLang")
	user := testutil.SeedUser(t, q, tenant.ID, "u@bilingual-lang.test", "pass")
	xliff, err := os.ReadFile("testdata/bilingual_de.xlf")
	if err != nil {
		t.Fatal(err)
	}
	po := "msgid \"\"\nmsgstr \"\"\n\"Content-Type: text/plain; charset=UTF-8\\n\"\n\nmsgid \"Settings\"\nmsgstr \"Einstellungen\"\n"

	// upload returns the active text per target language of the file's first
	// segment, and the upload's warnings.
	upload := func(t *testing.T, targets []string, name, content string) (map[string]string, string) {
		t.Helper()
		project := testutil.SeedProject(t, q, tenant.ID, name, "en", targets)
		rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID), name, "application/octet-stream", content, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusCreated)
		var resp struct {
			Data     db.File  `json:"data"`
			Warnings []string `json:"warnings"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		rows, err := pool.Query(ctx, `SELECT t.target_language, t.active_text FROM translations t
			JOIN segments s ON s.id = t.segment_id WHERE s.file_id = $1 AND s.position = 0`, resp.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]string{}
		for rows.Next() {
			var lang string
			var text *string
			if err := rows.Scan(&lang, &text); err != nil {
				t.Fatal(err)
			}
			got[lang] = ptr.Deref(text)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got, strings.Join(resp.Warnings, " | ")
	}

	got, warn := upload(t, []string{"de", "fr"}, "manual_de.xlf", string(xliff))
	if got["de"] != "Betriebsanleitung" || got["fr"] != "" || warn != "" {
		t.Errorf("German XLIFF in de+fr: %v, warnings %q; want de filled, fr empty", got, warn)
	}
	got, warn = upload(t, []string{"it", "fr"}, "manual_it.xlf", string(xliff))
	if got["it"] != "" || got["fr"] != "" || !strings.Contains(warn, "de-de") {
		t.Errorf("German XLIFF in it+fr: %v, warnings %q; want nothing filled and a warning naming de-de", got, warn)
	}
	got, warn = upload(t, []string{"de"}, "strings.po", po)
	if got["de"] != "Einstellungen" || warn != "" {
		t.Errorf("PO without a language in a de project: %v, warnings %q; want de filled", got, warn)
	}
	got, warn = upload(t, []string{"de", "fr"}, "strings2.po", po)
	if got["de"] != "" || got["fr"] != "" || warn == "" {
		t.Errorf("PO without a language in de+fr: %v, warnings %q; want nothing filled and a warning", got, warn)
	}
	// An exact match wins over a same-base one; two same-base targets and no
	// exact one is not a match.
	got, warn = upload(t, []string{"de", "de-de"}, "manual_exact.xlf", string(xliff))
	if got["de-de"] != "Betriebsanleitung" || got["de"] != "" || warn != "" {
		t.Errorf("German XLIFF in de+de-de: %v, warnings %q; want only de-de filled", got, warn)
	}
	got, warn = upload(t, []string{"de-at", "de-ch"}, "manual_ambiguous.xlf", string(xliff))
	if got["de-at"] != "" || got["de-ch"] != "" || !strings.Contains(warn, "de-de") {
		t.Errorf("German XLIFF in de-at+de-ch: %v, warnings %q; want nothing filled and a warning", got, warn)
	}
}
