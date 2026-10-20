package file_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/xuri/excelize/v2"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// A workbook without language codes in row 1 is translated cell by cell:
// upload makes a segment of every text cell of every visible sheet, and the
// job's export writes each translation back into its own cell.
func TestIntegrationUploadXLSXEveryTextCell(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouterWithStorage(pool, nil, file.NewMemoryStore())

	tenant := testutil.SeedTenant(t, q, "XLSXCellsCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "cells@test.com", "pass123")
	project := testutil.SeedProject(t, q, tenant.ID, "XLSXCellsProject", "en", []string{"fr"})

	f := excelize.NewFile()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetCellValue("Sheet1", "A1", "Product"))
	must(f.SetCellValue("Sheet1", "B1", 120)) // a number: not a segment
	must(f.SetCellValue("Sheet1", "C3", "Blue chair"))
	_, err := f.NewSheet("Prices")
	must(err)
	must(f.SetCellValue("Prices", "B2", "Free delivery"))
	_, err = f.NewSheet("Codes")
	must(err)
	must(f.SetCellValue("Codes", "A1", "Hidden list"))
	must(f.SetSheetVisible("Codes", false))
	var buf bytes.Buffer
	must(f.Write(&buf))
	f.Close()

	rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"cells.xlsx", buf.Bytes(), nil, user.ID, tenant.ID, "")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	rows, err := pool.Query(ctx, `
		SELECT s.segment_key, s.source_text, t.id
		FROM segments s JOIN translations t ON t.segment_id = s.id AND t.target_language = 'fr'
		WHERE s.file_id = $1 ORDER BY s.position`, resp.Data.ID)
	must(err)
	type seg struct {
		key, text string
		trID      int64
	}
	var segs []seg
	for rows.Next() {
		var s seg
		must(rows.Scan(&s.key, &s.text, &s.trID))
		segs = append(segs, s)
	}
	rows.Close()
	want := []seg{{key: "Sheet1!A1", text: "Product"}, {key: "Sheet1!C3", text: "Blue chair"}, {key: "Prices!B2", text: "Free delivery"}}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want %+v", segs, want)
	}
	for i := range want {
		if segs[i].key != want[i].key || segs[i].text != want[i].text {
			t.Errorf("segment %d = %s %q, want %s %q", i, segs[i].key, segs[i].text, want[i].key, want[i].text)
		}
	}

	// Translate two of the three; the third keeps its source in the export.
	for _, s := range segs[1:] {
		_, err := pool.Exec(ctx, `UPDATE translations SET active_text = $2, status = 'approved' WHERE id = $1`, s.trID, "FR "+s.text)
		must(err)
	}

	job, err := q.GetJobByFileAndLang(ctx, db.GetJobByFileAndLangParams{FileID: resp.Data.ID, TargetLanguage: "fr"})
	must(err)
	rec = testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/export", job.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	out, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	must(err)
	defer out.Close()
	for _, c := range []struct{ sheet, cell, want string }{
		{"Sheet1", "A1", "Product"},
		{"Sheet1", "B1", "120"},
		{"Sheet1", "C3", "FR Blue chair"},
		{"Prices", "B2", "FR Free delivery"},
		{"Codes", "A1", "Hidden list"},
		{"Sheet1", "D3", ""},
	} {
		if got, _ := out.GetCellValue(c.sheet, c.cell); got != c.want {
			t.Errorf("export %s!%s = %q, want %q", c.sheet, c.cell, got, c.want)
		}
	}
}
