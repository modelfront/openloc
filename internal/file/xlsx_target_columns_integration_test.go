package file_test

// An Excel filter profile with target columns: the upload reads only the
// source column, and each language's export writes its translations into the
// language's own column, keeping the source.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"openloc/internal/filterprofile"
	"openloc/internal/parser"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

const xlsxContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// catalogWorkbook has column titles in row 1, the source in B and French,
// German and Spanish columns in C, D and E, empty but for the extra cells.
func catalogWorkbook(t *testing.T, extra ...string) []byte {
	t.Helper()
	f := excelize.NewFile()
	cells := map[string]string{
		"A1": "ID", "B1": "Name", "C1": "French", "D1": "German", "E1": "Spanish",
		"A2": "P-1", "B2": "Blue chair",
		"A3": "P-2", "B3": "Red table",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		cells[extra[i]] = extra[i+1]
	}
	for cell, v := range cells {
		if err := f.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIntegrationXLSXTargetColumns_UploadAndExport(t *testing.T) {
	f := newSnapshotFixture(t, "XLSXTargets")
	ctx := context.Background()
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Catalog", "en", []string{"fr", "de", "es", "it", "fr-ca"})
	multilingual, err := filterprofile.NewService(f.q, f.pool).Create(ctx, f.tenant.ID, 0, filterprofile.Input{Name: "Multilingual", Format: "xlsx", Options: &filterprofile.Options{
		XLSX: &filterprofile.XLSXRules{HeaderRow: true, Columns: []string{"B"}, TargetColumns: []parser.XLSXTargetColumn{
			{Language: "fr", Column: "C"}, {Language: "de", Column: "D"}, {Language: "es", Column: "E"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filterprofile.SetChoices(ctx, f.q, f.tenant.ID, project.SettingsProfileID, map[string]*int64{"xlsx": &multilingual.ID}); err != nil {
		t.Fatal(err)
	}

	// The upload dialog starts from the inspect suggestion and sends it back
	// as the file's layout, which wins over the profile: it must carry the
	// target columns.
	rec := testutil.DoMultipartUpload(t, f.router, fmt.Sprintf("/api/projects/%d/files/inspect", project.ID), "catalog.xlsx",
		catalogWorkbook(t), nil, f.owner.ID, f.tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var inspected struct {
		Data struct {
			Suggested json.RawMessage `json:"suggested_layout"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &inspected)
	wantSuggested := `{"sheets":[{"name":"Sheet1","header_row":true,"columns":["B"],"target_columns":[{"language":"fr","column":"C"},{"language":"de","column":"D"},{"language":"es","column":"E"}]}]}`
	if got := string(inspected.Data.Suggested); got != wantSuggested {
		t.Errorf("suggested layout = %s, want %s", got, wantSuggested)
	}

	fileID := f.uploadAs(t, project.ID, "catalog.xlsx", xlsxContentType, string(catalogWorkbook(t)))
	if got, want := strings.Join(f.sources(t, fileID), "|"), "Blue chair|Red table"; got != want {
		t.Fatalf("segments = %q, want %q (source column only)", got, want)
	}
	var raw []byte
	if err := f.pool.QueryRow(ctx, `SELECT xlsx_layout FROM files WHERE id = $1`, fileID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var layout parser.XLSXLayout
	if err := json.Unmarshal(raw, &layout); err != nil || len(layout.Sheets) != 1 || len(layout.Sheets[0].TargetColumns) != 3 {
		t.Fatalf("stored layout = %s (%v), want Sheet1 with three target columns", raw, err)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE translations t SET active_text = upper(t.target_language) || ':' || s.source_text
		FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1`, fileID); err != nil {
		t.Fatal(err)
	}
	exports := pipeline.NewService(f.q, pipeline.NewPgxTxRunner(f.pool), f.store)
	for lang, want := range map[string]string{
		"fr": "B2=Blue chair|B3=Red table|C2=FR:Blue chair|C3=FR:Red table|D2=|D3=|E2=|E3=",
		"de": "B2=Blue chair|B3=Red table|C2=|C3=|D2=DE:Blue chair|D3=DE:Red table|E2=|E3=",
		"es": "B2=Blue chair|B3=Red table|C2=|C3=|D2=|D3=|E2=ES:Blue chair|E3=ES:Red table",
	} {
		out, _, err := exports.ExportFile(ctx, f.tenant.ID, fileID, lang)
		if err != nil {
			t.Fatalf("export %s: %v", lang, err)
		}
		if got := workbookCells(t, out, "B2", "B3", "C2", "C3", "D2", "D3", "E2", "E3"); got != want {
			t.Errorf("export %s cells = %q, want %q", lang, got, want)
		}
	}

	// Italian has no column: the export says so instead of overwriting the
	// source.
	_, _, err = exports.ExportFile(ctx, f.tenant.ID, fileID, "it")
	msg, ok := pipeline.UserExportMessage(err)
	if !ok || !strings.Contains(msg, "gives it no column") {
		t.Errorf("export it: err %v, message %q; want the no-column message", err, msg)
	}

	// The Files tab downloads the file's jobs together: one workbook with
	// every language in its column, not one copy per language.
	jobs := map[string]int64{}
	rows, err := f.pool.Query(ctx, `SELECT target_language, id FROM jobs WHERE file_id = $1`, fileID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var lang string
		var id int64
		if err := rows.Scan(&lang, &id); err != nil {
			t.Fatal(err)
		}
		jobs[lang] = id
	}
	if rows.Err() != nil || len(jobs) != 5 {
		t.Fatalf("jobs = %v (%v), want one per target language", jobs, rows.Err())
	}
	download := func(langs ...string) *httptest.ResponseRecorder {
		t.Helper()
		ids := make([]int64, 0, len(langs))
		for _, l := range langs {
			ids = append(ids, jobs[l])
		}
		return postDownload(t, f, project.ID, "source", ids)
	}
	rec = download("fr", "de", "es")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct == "application/zip" {
		t.Fatalf("three languages of one multilingual workbook came back as a zip")
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "catalog.xlsx") || strings.Contains(cd, "fr_") {
		t.Errorf("Content-Disposition = %q, want the file's own name", cd)
	}
	want := "B2=Blue chair|B3=Red table|C2=FR:Blue chair|C3=FR:Red table|D2=DE:Blue chair|D3=DE:Red table|E2=ES:Blue chair|E3=ES:Red table"
	if got := workbookCells(t, rec.Body.Bytes(), "B2", "B3", "C2", "C3", "D2", "D3", "E2", "E3"); got != want {
		t.Errorf("combined download cells = %q, want %q", got, want)
	}
	// The history keeps one export per language the workbook carries.
	var exported []string
	evRows, err := f.pool.Query(ctx, `SELECT details->>'target_language' FROM project_events
		WHERE project_id = $1 AND event_type = 'job.exported' AND file_id = $2 ORDER BY 1`, project.ID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	for evRows.Next() {
		var lang string
		if err := evRows.Scan(&lang); err != nil {
			t.Fatal(err)
		}
		exported = append(exported, lang)
	}
	if evRows.Err() != nil || strings.Join(exported, ",") != "de,es,fr" {
		t.Errorf("job.exported languages = %v (%v), want de, es and fr", exported, evRows.Err())
	}

	// Any other file holds one language: several at once would overwrite
	// each other, so they are refused.
	txtID := f.uploadText(t, project.ID, "notes.txt", "Hello")
	if _, _, err := exports.ExportFileLanguages(ctx, f.tenant.ID, txtID, []string{"fr", "de"}); err == nil {
		t.Error("two languages into one text file were accepted")
	}

	// A batch with another file's job is a zip: the workbook once, with both
	// its languages, and the text file per language.
	var txtFr int64
	if err := f.pool.QueryRow(ctx, `SELECT id FROM jobs WHERE file_id = $1 AND target_language = 'fr'`, txtID).Scan(&txtFr); err != nil {
		t.Fatal(err)
	}
	rec = postDownload(t, f, project.ID, "source", []int64{jobs["fr"], txtFr, jobs["de"]})
	testutil.AssertStatus(t, rec, http.StatusOK)
	entries := zipFiles(t, rec.Body.Bytes())
	if names := strings.Join(slices.Sorted(maps.Keys(entries)), ","); names != "catalog.xlsx,fr_notes.txt" {
		t.Fatalf("mixed batch entries = %s, want catalog.xlsx and fr_notes.txt", names)
	}
	if got := workbookCells(t, entries["catalog.xlsx"], "C2", "D2", "E2"); got != "C2=FR:Blue chair|D2=DE:Blue chair|E2=" {
		t.Errorf("mixed batch workbook cells = %q", got)
	}

	// The bilingual XLIFF stays one file per language.
	rec = postDownload(t, f, project.ID, "xliff", []int64{jobs["fr"], jobs["de"]})
	testutil.AssertStatus(t, rec, http.StatusOK)
	if n := len(zipFiles(t, rec.Body.Bytes())); n != 2 {
		t.Errorf("xliff download of fr and de holds %d files, want 2", n)
	}

	// The jobs list tells the Files tab which file downloads as one workbook.
	rec = testutil.DoRequest(t, f.router, "GET", fmt.Sprintf("/api/projects/%d/jobs", project.ID), nil, f.owner.ID, f.tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var listed struct {
		Data []struct {
			FileID           int64 `json:"file_id"`
			FileMultilingual bool  `json:"file_multilingual"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &listed)
	for _, j := range listed.Data {
		if j.FileMultilingual != (j.FileID == fileID) {
			t.Errorf("job of file %d: file_multilingual = %v", j.FileID, j.FileMultilingual)
		}
	}
	if len(listed.Data) != 10 {
		t.Errorf("listed %d jobs, want 5 per file", len(listed.Data))
	}

	// One language alone keeps the per-language name.
	rec = download("de")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "de_catalog.xlsx") {
		t.Errorf("one-language Content-Disposition = %q, want de_catalog.xlsx", cd)
	}

	// fr-CA has no column of its own and writes into fr's: together they would
	// overwrite each other, so the combined download refuses them.
	rec = download("fr", "fr-ca")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "catalog.xlsx") || !strings.Contains(body, "fr and fr-ca both go into column C") {
		t.Errorf("download with fr and fr-ca = %s, want the shared-column message", body)
	}

	// A language with no column stops the combined download, naming the file.
	rec = download("fr", "it")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "catalog.xlsx") || !strings.Contains(body, "gives it no column") {
		t.Errorf("download with it = %s, want the file name and the no-column message", body)
	}
}

func workbookCells(t *testing.T, content []byte, cells ...string) string {
	t.Helper()
	wb, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Close()
	var out []string
	for _, c := range cells {
		v, err := wb.GetCellValue("Sheet1", c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c+"="+v)
	}
	return strings.Join(out, "|")
}

func postDownload(t *testing.T, f snapshotFixture, projectID int64, format string, jobIDs []int64) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/download", projectID),
		map[string]any{"format": format, "job_ids": jobIDs}, f.owner.ID, f.tenant.ID)
}

// zipFiles reads a zip download: entry name → content.
func zipFiles(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string][]byte{}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[zf.Name] = data
	}
	return out
}

// Text already in a language column becomes that language's translation,
// to review: imported from the file, not approved.
func TestIntegrationXLSXTargetColumns_ImportsExistingTranslations(t *testing.T) {
	f := newSnapshotFixture(t, "XLSXTargetsImport")
	ctx := context.Background()
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Catalog", "en", []string{"fr", "de"})
	profile, err := filterprofile.NewService(f.q, f.pool).Create(ctx, f.tenant.ID, 0, filterprofile.Input{Name: "Multilingual", Format: "xlsx", Options: &filterprofile.Options{
		XLSX: &filterprofile.XLSXRules{HeaderRow: true, Columns: []string{"B"}, TargetColumns: []parser.XLSXTargetColumn{
			{Language: "fr", Column: "C"}, {Language: "de", Column: "D"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filterprofile.SetChoices(ctx, f.q, f.tenant.ID, project.SettingsProfileID, map[string]*int64{"xlsx": &profile.ID}); err != nil {
		t.Fatal(err)
	}
	fileID := f.uploadAs(t, project.ID, "catalog.xlsx", xlsxContentType, string(catalogWorkbook(t, "D2", "Blauer Stuhl")))

	rows, err := f.pool.Query(ctx, `SELECT s.source_text, t.target_language, COALESCE(t.active_text, ''), t.status::text, COALESCE(t.origin::text, '')
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 ORDER BY s.position, t.target_language`, fileID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var src, lang, text, status, origin string
		if err := rows.Scan(&src, &lang, &text, &status, &origin); err != nil {
			t.Fatal(err)
		}
		if text != "" {
			got = append(got, fmt.Sprintf("%s/%s=%s (%s, %s)", src, lang, text, status, origin))
		}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if want := "Blue chair/de=Blauer Stuhl (needs_review, file_import)"; strings.Join(got, "|") != want {
		t.Errorf("imported translations = %q, want %q", got, want)
	}
}
