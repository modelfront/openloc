package mcp_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"openloc/internal/file"
)

// openloc_attach_uploaded_file takes the same Excel column layout as the
// upload form: only the chosen column of the chosen sheet is segmented.
func TestIntegrationMCPAttachXLSXWithLayout(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	wb := excelize.NewFile()
	defer wb.Close()
	for cell, v := range map[string]string{"A1": "ID", "B1": "English", "A2": "P-1", "B2": "Blue chair", "A3": "P-2", "B3": "Red table"} {
		if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := wb.Write(&buf); err != nil {
		t.Fatal(err)
	}

	layout := map[string]any{"sheets": []map[string]any{{
		"name": "Sheet1", "header_row": true, "columns": []string{"B"},
	}}}
	upload := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "catalog.xlsx", buf.Bytes())
	var attached struct {
		File fileOut `json:"file"`
	}
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{
		"project_id": f.projectA.ID, "upload_id": upload, "xlsx_layout": layout,
	}, &attached); isErr {
		t.Fatalf("attach_uploaded_file: %s", text)
	}

	got := segmentKeys(t, f, attached.File.ID)
	if want := []string{"Sheet1!B2=Blue chair", "Sheet1!B3=Red table"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("segments = %q, want %q", got, want)
	}

	// The tool refuses a layout the workbook cannot take. header_row is
	// optional, as in the upload form: the refusal is the sheet's.
	upload = stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "catalog2.xlsx", buf.Bytes())
	layout["sheets"] = []map[string]any{{"name": "Missing", "columns": []string{"B"}}}
	isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{
		"project_id": f.projectA.ID, "upload_id": upload, "xlsx_layout": layout,
	}, nil)
	if !isErr || !strings.Contains(text, "has no sheet named") {
		t.Errorf("attach with a layout naming a missing sheet = %v %q, want the missing-sheet refusal", isErr, text)
	}
}

// segmentKeys lists a file's segments as key=source, in order.
func segmentKeys(t *testing.T, f *fixture, fileID int64) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT segment_key || '=' || source_text FROM segments WHERE file_id = $1 ORDER BY position`, fileID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var kv string
		if err := rows.Scan(&kv); err != nil {
			t.Fatal(err)
		}
		got = append(got, kv)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// openloc_attach_uploaded_files applies each file's own layout.
func TestIntegrationMCPAttachXLSXBatchLayoutPerFile(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	wb := excelize.NewFile()
	defer wb.Close()
	for cell, v := range map[string]string{"A1": "Name", "B1": "Description", "A2": "Blue chair", "B2": "Made of oak", "A3": "Red table", "B3": "Seats four"} {
		if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := wb.Write(&buf); err != nil {
		t.Fatal(err)
	}
	column := func(col string) map[string]any {
		return map[string]any{"sheets": []map[string]any{{"name": "Sheet1", "header_row": true, "columns": []string{col}}}}
	}
	names := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "names.xlsx", buf.Bytes())
	descriptions := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "descriptions.xlsx", buf.Bytes())

	var out attachedFilesOut
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
		"project_id": f.projectA.ID,
		"files": []map[string]any{
			{"upload_id": names, "xlsx_layout": column("A")},
			{"upload_id": descriptions, "xlsx_layout": column("B")},
		},
	}, &out); isErr || out.Added != 2 {
		t.Fatalf("attach_uploaded_files: %v %s %+v", isErr, text, out)
	}
	for i, want := range [][]string{
		{"Sheet1!A2=Blue chair", "Sheet1!A3=Red table"},
		{"Sheet1!B2=Made of oak", "Sheet1!B3=Seats four"},
	} {
		got := segmentKeys(t, f, out.Files[i].File.ID)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("file %d segments = %q, want %q", i, got, want)
		}
	}
}
