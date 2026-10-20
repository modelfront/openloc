package file_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// An Excel workbook without language codes in row 1 can be uploaded with a
// column layout: per chosen sheet, its chosen columns or every text cell are
// segmented, and the export writes each translation back into its own cell.

func layoutWorkbookBytes(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Products"))
	for cell, v := range map[string]any{
		"A1": "ID", "B1": "English", "A2": "P-1", "B2": "Blue chair", "C2": "Bestseller", "A3": "P-2", "B3": "Red table",
	} {
		must(f.SetCellValue("Products", cell, v))
	}
	_, err := f.NewSheet("Notes")
	must(err)
	must(f.SetCellValue("Notes", "A1", "Handle with care"))
	must(f.SetCellValue("Notes", "B2", "Keep dry"))
	var buf bytes.Buffer
	must(f.Write(&buf))
	return buf.Bytes()
}

// testLayoutJSON translates the English column of Products and the whole of
// Notes.
const testLayoutJSON = `{"sheets":[{"name":"Products","header_row":true,"columns":["B"]},{"name":"Notes"}]}`

const testLayoutSegments = "Products!B2=Blue chair|Products!B3=Red table|Notes!A1=Handle with care|Notes!B2=Keep dry"

func (f draftUploadFixture) segmentKeys(t *testing.T, fileID int64) string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT segment_key, source_text FROM segments WHERE file_id = $1 ORDER BY position`, fileID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key, text string
		if err := rows.Scan(&key, &text); err != nil {
			t.Fatal(err)
		}
		out = append(out, key+"="+text)
	}
	return strings.Join(out, "|")
}

func (f draftUploadFixture) uploadLayout(t *testing.T, layout string) int64 {
	t.Helper()
	rec := testutil.DoMultipartUpload(t, f.router, fmt.Sprintf("/api/projects/%d/files", f.draftID), "catalog.xlsx",
		layoutWorkbookBytes(t), map[string]string{"xlsx_layout": layout}, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID         int64           `json:"id"`
			XLSXLayout json.RawMessage `json:"xlsx_layout"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if !strings.Contains(string(resp.Data.XLSXLayout), `"Products"`) {
		t.Fatalf("upload response xlsx_layout = %s, want the layout as JSON", resp.Data.XLSXLayout)
	}
	return resp.Data.ID
}

func (f draftUploadFixture) setLayout(t *testing.T, fileID int64, layout string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "PUT", fmt.Sprintf("/api/projects/%d/files/%d/xlsx-layout", f.draftID, fileID),
		json.RawMessage(layout), f.pm.ID, f.tenant.ID, "project_manager")
}

func (f draftUploadFixture) publish(t *testing.T) {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
		map[string]any{"analyze": false}, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// approveAndExport approves every segment of the file's lang job with
// "<lang>:<source>" and returns the exported workbook.
func (f draftUploadFixture) approveAndExport(t *testing.T, fileID int64, lang string) *excelize.File {
	t.Helper()
	ctx := context.Background()
	job, err := f.q.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: fileID, TargetLanguage: lang})
	if err != nil {
		t.Fatalf("job %s: %v", lang, err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE translations t SET active_text = $2 || ':' || s.source_text, status = 'approved'
		FROM segments s WHERE s.id = t.segment_id AND s.file_id = $1 AND t.target_language = $2`, fileID, lang); err != nil {
		t.Fatal(err)
	}
	rec := testutil.DoRequestWithRole(t, f.router, "GET", fmt.Sprintf("/api/jobs/%d/export", job.ID), nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	out, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("open export: %v", err)
	}
	t.Cleanup(func() { out.Close() })
	return out
}

func cellIs(t *testing.T, f *excelize.File, sheet, cell, want string) {
	t.Helper()
	if got, _ := f.GetCellValue(sheet, cell); got != want {
		t.Errorf("%s!%s = %q, want %q", sheet, cell, got, want)
	}
}

func TestIntegrationXLSXInspect(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXInspect")
	path := fmt.Sprintf("/api/projects/%d/files/inspect", f.draftID)

	rec := testutil.DoMultipartUpload(t, f.router, path, "catalog.xlsx", layoutWorkbookBytes(t), nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data struct {
			Bilingual bool `json:"bilingual"`
			Sheets    []struct {
				Name    string `json:"name"`
				Columns []struct {
					Letter string `json:"letter"`
					Title  string `json:"title"`
				} `json:"columns"`
			} `json:"sheets"`
			Suggested struct {
				Sheets []struct {
					Name    string   `json:"name"`
					Columns []string `json:"columns"`
				} `json:"sheets"`
			} `json:"suggested_layout"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	d := resp.Data
	if d.Bilingual || len(d.Sheets) != 2 || len(d.Sheets[0].Columns) < 2 || d.Sheets[0].Columns[1].Letter != "B" || d.Sheets[0].Columns[1].Title != "English" {
		t.Errorf("inspection = %+v", d)
	}
	if s := d.Suggested.Sheets; len(s) != 2 || strings.Join(s[0].Columns, ",") != "B" || strings.Join(s[1].Columns, ",") != "A" {
		t.Errorf("suggested = %+v, want the column with the most text of each sheet", s)
	}
	// Nothing is stored.
	if n := f.count(t, `SELECT count(*) FROM files WHERE project_id = $1`, f.draftID); n != 0 {
		t.Errorf("inspect stored %d files", n)
	}

	rec = testutil.DoMultipartUpload(t, f.router, path, "notes.txt", []byte("Hello"), nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	linguist := testutil.SeedUserWithRole(t, f.q, f.tenant.ID, "ling@xlsxinspect.test", "pass", "linguist")
	rec = testutil.DoMultipartUpload(t, f.router, path, "catalog.xlsx", layoutWorkbookBytes(t), nil, linguist.ID, f.tenant.ID, "linguist")
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("linguist inspect = %d, want 403 or 404", rec.Code)
	}

	other := testutil.SeedTenant(t, f.q, "XLSXInspectOther")
	otherPM := testutil.SeedUserWithRole(t, f.q, other.ID, "pm@xlsxinspectother.test", "pass", "project_manager")
	rec = testutil.DoMultipartUpload(t, f.router, path, "catalog.xlsx", layoutWorkbookBytes(t), nil, otherPM.ID, other.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestIntegrationXLSXLayoutUploadPublishExport(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutFlow")
	fileID := f.uploadLayout(t, testLayoutJSON)
	if got := f.segmentKeys(t, fileID); got != testLayoutSegments {
		t.Fatalf("segments %q, want %q", got, testLayoutSegments)
	}

	f.publish(t)
	// Publish re-reads the stored file by its layout.
	if got := f.segmentKeys(t, fileID); got != testLayoutSegments {
		t.Fatalf("segments after publish %q, want %q", got, testLayoutSegments)
	}

	// Each job's file has its translations in the source cells.
	de := f.approveAndExport(t, fileID, "de")
	cellIs(t, de, "Products", "B2", "de:Blue chair")
	cellIs(t, de, "Products", "B3", "de:Red table")
	cellIs(t, de, "Notes", "A1", "de:Handle with care")
	cellIs(t, de, "Notes", "B2", "de:Keep dry")
	cellIs(t, de, "Products", "B1", "English")
	cellIs(t, de, "Products", "C2", "Bestseller")
	cellIs(t, de, "Products", "A2", "P-1")
	cellIs(t, de, "Products", "D2", "")

	fr := f.approveAndExport(t, fileID, "fr")
	cellIs(t, fr, "Products", "B3", "fr:Red table")
}

func TestIntegrationXLSXLayoutChanges(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutChange")
	fileID := f.uploadLayout(t, testLayoutJSON)

	// In a draft the layout may change: the file is segmented again.
	productsOnly := `{"sheets":[{"name":"Products","header_row":true,"columns":["B"]}]}`
	testutil.AssertStatus(t, f.setLayout(t, fileID, productsOnly), http.StatusOK)
	if got, want := f.segmentKeys(t, fileID), "Products!B2=Blue chair|Products!B3=Red table"; got != want {
		t.Fatalf("after the draft change segments %q, want %q", got, want)
	}
	wholeSheet := `{"sheets":[{"name":"Products","header_row":true}]}`
	testutil.AssertStatus(t, f.setLayout(t, fileID, wholeSheet), http.StatusOK)
	if got, want := f.segmentKeys(t, fileID), "Products!A2=P-1|Products!B2=Blue chair|Products!C2=Bestseller|Products!A3=P-2|Products!B3=Red table"; got != want {
		t.Fatalf("whole sheet segments %q, want %q", got, want)
	}
	testutil.AssertStatus(t, f.setLayout(t, fileID, `{"sheets":[{"name":"Products","columns":["1A"]}]}`), http.StatusBadRequest)
	testutil.AssertStatus(t, f.setLayout(t, fileID, productsOnly), http.StatusOK)

	// Published: the segments sit under jobs, so the layout is fixed.
	f.publish(t)
	rec := f.setLayout(t, fileID, `{"sheets":[{"name":"Products","header_row":true,"columns":["A"]}]}`)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if got, want := f.segmentKeys(t, fileID), "Products!B2=Blue chair|Products!B3=Red table"; got != want {
		t.Errorf("a refused change re-segmented the file: %q", got)
	}
}

func TestIntegrationXLSXLayoutRefusedUploads(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutRefused")
	path := fmt.Sprintf("/api/projects/%d/files", f.draftID)
	for name, c := range map[string]struct {
		filename, layout string
		content          []byte
		msg              string
	}{
		"not JSON":         {"catalog.xlsx", `{`, layoutWorkbookBytes(t), "The Excel column layout is not valid JSON."},
		"no sheets":        {"catalog.xlsx", `{"sheets":[]}`, layoutWorkbookBytes(t), "Choose at least one sheet to translate."},
		"unknown sheet":    {"catalog.xlsx", `{"sheets":[{"name":"Nope","columns":["A"]}]}`, layoutWorkbookBytes(t), "has no sheet named"},
		"not a column":     {"catalog.xlsx", `{"sheets":[{"name":"Notes","columns":["1A"]}]}`, layoutWorkbookBytes(t), "is not a column"},
		"empty column":     {"catalog.xlsx", `{"sheets":[{"name":"Notes","columns":["Z"]}]}`, layoutWorkbookBytes(t), "The chosen sheets and columns hold no text to translate."},
		"layout on a .txt": {"notes.txt", testLayoutJSON, []byte("Hello"), "applies to .xlsx files only"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := testutil.DoMultipartUpload(t, f.router, path, c.filename, c.content, map[string]string{"xlsx_layout": c.layout}, f.pm.ID, f.tenant.ID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusBadRequest)
			if !strings.Contains(rec.Body.String(), c.msg) {
				t.Errorf("body = %s, want %q", rec.Body.String(), c.msg)
			}
		})
	}
	if n := f.count(t, `SELECT count(*) FROM files WHERE project_id = $1`, f.draftID); n != 0 {
		t.Errorf("refused uploads stored %d files", n)
	}
}

// The layout read returns the stored layout with the stored workbook's
// columns; neither route reaches a file of another tenant or another project.
func TestIntegrationXLSXLayoutReadAndTenantScope(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutRead")
	fileID := f.uploadLayout(t, testLayoutJSON)
	path := fmt.Sprintf("/api/projects/%d/files/%d/xlsx-layout", f.draftID, fileID)

	rec := testutil.DoRequestWithRole(t, f.router, "GET", path, nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var got struct {
		Data struct {
			Layout struct {
				Sheets []struct {
					Name    string   `json:"name"`
					Columns []string `json:"columns"`
				} `json:"sheets"`
			} `json:"layout"`
			Inspection struct {
				Sheets []struct {
					Name    string `json:"name"`
					Columns []struct {
						Letter string `json:"letter"`
						Title  string `json:"title"`
					} `json:"columns"`
				} `json:"sheets"`
			} `json:"inspection"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &got)
	if l := got.Data.Layout.Sheets; len(l) == 0 || l[0].Name != "Products" || strings.Join(l[0].Columns, ",") != "B" {
		t.Errorf("layout = %+v, want the stored one", l)
	}
	if s := got.Data.Inspection.Sheets; len(s) == 0 || s[0].Name != "Products" || len(s[0].Columns) < 2 || s[0].Columns[1].Title != "English" {
		t.Errorf("inspection = %+v, want the stored workbook's columns", s)
	}

	other := testutil.SeedTenant(t, f.q, "XLSXLayoutReadOther")
	otherPM := testutil.SeedUserWithRole(t, f.q, other.ID, "pm@xlsxlayoutreadother.test", "pass", "project_manager")
	otherProject := testutil.SeedProject(t, f.q, other.ID, "Other", "en", []string{"fr"})
	sameTenantProject := testutil.SeedProject(t, f.q, f.tenant.ID, "Second", "en", []string{"fr"})
	before, storedBefore := f.segmentKeys(t, fileID), f.storedLayout(t, fileID)
	// A layout unlike the stored one: a change that got through would show.
	changed := json.RawMessage(`{"sheets":[{"name":"Products","header_row":true,"columns":["A"]}]}`)
	for name, c := range map[string]struct {
		path             string
		userID, tenantID int64
	}{
		"another tenant":                   {path, otherPM.ID, other.ID},
		"another tenant's project in path": {fmt.Sprintf("/api/projects/%d/files/%d/xlsx-layout", otherProject.ID, fileID), otherPM.ID, other.ID},
		"another project of the tenant":    {fmt.Sprintf("/api/projects/%d/files/%d/xlsx-layout", sameTenantProject.ID, fileID), f.pm.ID, f.tenant.ID},
	} {
		t.Run(name, func(t *testing.T) {
			rec := testutil.DoRequestWithRole(t, f.router, "GET", c.path, nil, c.userID, c.tenantID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusNotFound)
			rec = testutil.DoRequestWithRole(t, f.router, "PUT", c.path, changed, c.userID, c.tenantID, "project_manager")
			testutil.AssertStatus(t, rec, http.StatusNotFound)
		})
	}
	if got := f.segmentKeys(t, fileID); got != before {
		t.Errorf("segments after the refused requests = %q, want %q", got, before)
	}
	if got := f.storedLayout(t, fileID); got != storedBefore {
		t.Errorf("stored layout after the refused requests = %s, want %s", got, storedBefore)
	}
}

// A publish that failed part-way leaves the project a draft with some files
// already under jobs. Their layout is fixed like a published file's: a new
// one would drop the segments the jobs' translations hang on.
func TestIntegrationXLSXLayoutRefusedOnceTheFileHasJobs(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutJobs")
	fileID := f.uploadLayout(t, testLayoutJSON)
	before := f.segmentKeys(t, fileID)
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO jobs (file_id, target_language) VALUES ($1, 'fr')`, fileID); err != nil {
		t.Fatal(err)
	}

	rec := f.setLayout(t, fileID, `{"sheets":[{"name":"Products","header_row":true,"columns":["A"]}]}`)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if got := f.segmentKeys(t, fileID); got != before {
		t.Errorf("segments after the refused change = %q, want %q", got, before)
	}
}

// A workbook with language codes in row 1 is read by them: an upload or a
// change that sends a column layout for it is refused with a message.
func TestIntegrationXLSXLayoutRefusedForABilingualWorkbook(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutBilingual")
	path := fmt.Sprintf("/api/projects/%d/files", f.draftID)
	bilingual := buildXLSXBilingual(t, []string{"en", "fr"}, [][]string{{"Hello", "Bonjour"}})
	const want = "This workbook has language codes in row 1, so it is read by them. Upload it without a column layout."

	rec := testutil.DoMultipartUpload(t, f.router, path, "bilingual.xlsx", bilingual,
		map[string]string{"xlsx_layout": `{"sheets":[{"name":"Sheet1","columns":["A"]}]}`}, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("upload body = %s, want %q", rec.Body.String(), want)
	}

	// Uploaded without a layout it is read by its codes; a layout cannot be put on it later.
	rec = testutil.DoMultipartUpload(t, f.router, path, "bilingual.xlsx", bilingual, nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	before := f.segmentKeys(t, resp.Data.ID)
	rec = f.setLayout(t, resp.Data.ID, `{"sheets":[{"name":"Sheet1","columns":["A"]}]}`)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("PUT body = %s, want %q", rec.Body.String(), want)
	}
	if got := f.segmentKeys(t, resp.Data.ID); got != before {
		t.Errorf("segments after the refused PUT = %q, want %q", got, before)
	}
}

// A layout change that waits on a publish holding the file row sees the jobs
// that publish commits, and is refused: its segments now sit under jobs.
func TestIntegrationXLSXLayoutWaitsForAPublishHoldingTheFile(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXLayoutRace")
	ctx := context.Background()
	fileID := f.uploadLayout(t, testLayoutJSON)
	before := f.segmentKeys(t, fileID)
	storedBefore := f.storedLayout(t, fileID)

	// The test plays the publish: it holds the row, then commits a job.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(ctx, `SELECT id FROM files WHERE id = $1 FOR UPDATE`, fileID); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.setLayout(t, fileID, `{"sheets":[{"name":"Products","header_row":true,"columns":["A"]}]}`)
	}()
	var early *httptest.ResponseRecorder
	for deadline := time.Now().Add(10 * time.Second); early == nil && testutil.LockWaiters(t, f.pool) == 0; time.Sleep(10 * time.Millisecond) {
		select {
		case early = <-done: // the change did not wait for the row
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the layout change to wait on the file row")
		}
	}

	if _, err := tx.Exec(ctx, `INSERT INTO jobs (file_id, target_language) VALUES ($1, 'fr')`, fileID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rec := early
	if rec == nil {
		rec = <-done
	}
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if got := f.segmentKeys(t, fileID); got != before {
		t.Errorf("segments after the refused change = %q, want %q", got, before)
	}
	if got := f.storedLayout(t, fileID); got != storedBefore {
		t.Errorf("stored layout after the refused change = %s, want %s", got, storedBefore)
	}
}

// storedLayout is the file's xlsx_layout as stored.
func (f draftUploadFixture) storedLayout(t *testing.T, fileID int64) string {
	t.Helper()
	var layout []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT xlsx_layout FROM files WHERE id = $1`, fileID).Scan(&layout); err != nil {
		t.Fatal(err)
	}
	return string(layout)
}

// A workbook, or a choice of columns, with no text to translate is refused
// with a message that says so, never as a broken file.
func TestIntegrationXLSXNoTextIsSaid(t *testing.T) {
	f := newDraftUploadFixture(t, "XLSXNoText")
	path := fmt.Sprintf("/api/projects/%d/files", f.draftID)

	numbers := excelize.NewFile()
	if err := numbers.SetCellValue("Sheet1", "A1", 42); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := numbers.Write(&buf); err != nil {
		t.Fatal(err)
	}
	numbers.Close()
	rec := testutil.DoMultipartUpload(t, f.router, path, "numbers.xlsx", buf.Bytes(), nil, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if want := "The workbook has no text to translate on its visible sheets."; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("upload body = %s, want %q", rec.Body.String(), want)
	}

	fileID := f.uploadLayout(t, testLayoutJSON)
	before, storedBefore := f.segmentKeys(t, fileID), f.storedLayout(t, fileID)
	rec = f.setLayout(t, fileID, `{"sheets":[{"name":"Products","header_row":true,"columns":["Z"]}]}`)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if want := "The chosen sheets and columns hold no text to translate."; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("PUT body = %s, want %q", rec.Body.String(), want)
	}
	if got := f.segmentKeys(t, fileID); got != before {
		t.Errorf("segments after the refused change = %q, want %q", got, before)
	}
	if got := f.storedLayout(t, fileID); got != storedBefore {
		t.Errorf("stored layout after the refused change = %s, want %s", got, storedBefore)
	}
}
