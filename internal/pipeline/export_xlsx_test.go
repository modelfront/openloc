package pipeline

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/xuri/excelize/v2"

	db "openloc/internal/database/sqlcgen"
)

// memStorage serves the same stored bytes for any path.
type memStorage []byte

func (m memStorage) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m)), nil
}

// Each job of a workbook with several target columns exports its approved
// text into its own language's column; the other columns stay as uploaded.
func TestExportFile_XLSXWritesJobLanguageColumn(t *testing.T) {
	wb := excelize.NewFile()
	for cell, v := range map[string]string{
		"A1": "en", "B1": "fr", "C1": "de", "D1": "it",
		"A2": "Hello", "B2": "Bonjour", "C2": "Hallo", "D2": "Ciao",
	} {
		if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	var upload bytes.Buffer
	if err := wb.Write(&upload); err != nil {
		t.Fatal(err)
	}
	wb.Close()

	const tenantID, fileID = 1, 1
	mq := newMockQuerier()
	mq.addFileProject(fileID, 1, nil, "en", []string{"fr", "de", "it"})
	file := mq.files[fileID]
	file.Format = db.FileFormatXlsx
	file.Name = "strings.xlsx"
	mq.files[fileID] = file

	jobs := []struct{ lang, cell, text string }{
		{"fr", "B2", "Salut"},
		{"de", "C2", "Servus"},
		{"it", "D2", "Salve"},
	}
	key := "row_2"
	for _, j := range jobs {
		text := j.text
		mq.approvedForExport[fileLangKey(fileID, j.lang)] = []db.ListApprovedForExportRow{
			{SegmentKey: &key, ActiveText: &text, Status: "approved"},
		}
	}
	svc := NewService(mq, &passthroughTxRunner{q: mq}, memStorage(upload.Bytes()))

	uploaded := map[string]string{"B2": "Bonjour", "C2": "Hallo", "D2": "Ciao"}
	for _, j := range jobs {
		t.Run(j.lang, func(t *testing.T) {
			out, _, err := svc.ExportFile(context.Background(), tenantID, fileID, j.lang)
			if err != nil {
				t.Fatalf("ExportFile: %v", err)
			}
			f, err := excelize.OpenReader(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("open exported workbook: %v", err)
			}
			defer f.Close()
			got := map[string]string{}
			for cell := range uploaded {
				if got[cell], err = f.GetCellValue("Sheet1", cell); err != nil {
					t.Fatalf("read %s: %v", cell, err)
				}
			}
			for cell, want := range uploaded {
				if cell == j.cell {
					want = j.text
				}
				if got[cell] != want {
					t.Errorf("%s export: %s = %q, want %q (row 2: B=%q C=%q D=%q)",
						j.lang, cell, got[cell], want, got["B2"], got["C2"], got["D2"])
				}
			}
		})
	}
}
