package file_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/parser/pptxtest"
	"openloc/internal/testutil"
)

// An uploaded deck stores one segment per sentence, in reading order, each
// with the place it comes from and its chips.
func TestIntegrationUploadPPTX_StoresSegments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "DeckCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "deck@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "Decks", "en", []string{"fr"})

	rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID), "results.pptx", pptxtest.Build(pptxtest.JourneyDeck()), nil, user.ID, tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID     int64  `json:"id"`
			Format string `json:"format"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Format != "pptx" {
		t.Errorf("format = %q, want pptx", resp.Data.Format)
	}

	rows, err := pool.Query(ctx, `
		SELECT position, source_text, COALESCE(context, ''), COALESCE(source_rich_text, '')
		FROM segments WHERE file_id = $1 ORDER BY position`, resp.Data.ID)
	if err != nil {
		t.Fatalf("query segments: %v", err)
	}
	defer rows.Close()
	type seg struct {
		pos           int
		text, context string
		rich          string
	}
	var got []seg
	for rows.Next() {
		var s seg
		if err := rows.Scan(&s.pos, &s.text, &s.context, &s.rich); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("stored %d segments, want 10", len(got))
	}
	checks := map[int]seg{
		0: {0, "Quarterly results", "slide:1", ""},
		2: {2, "Costs fell.", "slide:1", `<g id="1" ctype="bold">Costs</g> fell.`},
		5: {5, "Bar chart of revenue by region", "slide:2/alt", ""},
		6: {6, "Mention the new office.", "slide:2/notes", ""},
		9: {9, "Launch", "slide:3/smartart", ""},
	}
	for i, want := range checks {
		if got[i] != want {
			t.Errorf("segment %d = %+v, want %+v", i, got[i], want)
		}
	}
}

// cfbFile is an OLE compound file naming one stream: what Office writes for a
// password-protected file ("EncryptedPackage") or a pre-2007 binary file
// ("PowerPoint Document").
func cfbFile(stream string) []byte {
	b := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 504)...)
	for _, r := range stream {
		b = append(b, byte(r), 0)
	}
	return b
}

// A file the user can fix gets a message that says how; any other parse
// failure keeps the generic one.
func TestIntegrationUpload_OOXMLErrorsSayHowToFix(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "FixCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "fix@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "Fixes", "en", []string{"fr"})

	noText := pptxtest.Build(pptxtest.Deck{Slides: []pptxtest.Slide{{Shapes: pptxtest.TextBox(2, pptxtest.P())}}})
	cases := []struct {
		filename string
		content  []byte
		want     string
	}{
		{"old.ppt", cfbFile("PowerPoint Document"), "save it as .pptx"},
		{"renamed.pptx", cfbFile("PowerPoint Document"), "save it as .pptx"},
		{"locked.pptx", cfbFile("EncryptedPackage"), "presentation is password-protected"},
		{"broken.pptx", []byte("not a zip"), "not a valid PowerPoint presentation (.pptx)"},
		{"blank.pptx", noText, "presentation has no text to translate"},
		{"locked.xlsx", cfbFile("EncryptedPackage"), "workbook is password-protected"},
		{"old.xls", cfbFile("Workbook"), "save it as .xlsx"},
		{"renamed.xlsx", cfbFile("Workbook"), "save it as .xlsx"},
		{"broken.xlsx", []byte("not a zip"), "not a valid Excel workbook (.xlsx)"},
		{"blank.xlsx", buildXLSXBilingual(t, []string{"en", "fr"}, nil), "workbook has no text to translate"},
		{"broken.json", []byte("{not json"), "could not parse json_i18n file"},
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID), tc.filename, tc.content, nil, user.ID, tenant.ID, "")
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body %s, want it to contain %q", rec.Body.String(), tc.want)
			}
		})
	}
}
