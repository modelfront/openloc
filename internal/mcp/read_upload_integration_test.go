package mcp_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"openloc/internal/file"
	"openloc/internal/testutil"
)

type uploadTextOut struct {
	Filename string `json:"filename"`
	Format   string `json:"format"`
	Text     string `json:"text"`
}

// openloc_read_uploaded_file returns the text of a file the caller attached,
// through the same parsers as a project upload, on a read-only token.
func TestIntegrationMCPReadUploadedFile(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	s := connect(t, f.url, f.tokenA)
	stage := func(name string, body []byte) string {
		return stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, name, body)
	}
	read := func(uploadID string, out *uploadTextOut) (bool, string) {
		return callTool(t, s, "openloc_read_uploaded_file", map[string]any{"upload_id": uploadID}, out)
	}

	t.Run("reads a text file", func(t *testing.T) {
		var out uploadTextOut
		if isErr, text := read(stage("style-guide.txt", []byte("Use the formal you.\nKeep product names in English.\n")), &out); isErr {
			t.Fatalf("read_uploaded_file: %s", text)
		}
		if out.Filename != "style-guide.txt" || out.Format != "txt" ||
			!strings.Contains(out.Text, "Use the formal you.") || !strings.Contains(out.Text, "Keep product names in English.") {
			t.Fatalf("result: %+v", out)
		}
	})

	t.Run("reads a workbook through its parser", func(t *testing.T) {
		wb := excelize.NewFile()
		defer wb.Close()
		for cell, v := range map[string]string{"A1": "Term", "B1": "Rule", "A2": "checkout", "B2": "Never translate"} {
			if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
				t.Fatal(err)
			}
		}
		var buf bytes.Buffer
		if err := wb.Write(&buf); err != nil {
			t.Fatal(err)
		}
		var out uploadTextOut
		if isErr, text := read(stage("rules.xlsx", buf.Bytes()), &out); isErr {
			t.Fatalf("read_uploaded_file: %s", text)
		}
		if out.Format != "xlsx" || !strings.Contains(out.Text, "checkout") || !strings.Contains(out.Text, "Never translate") {
			t.Fatalf("result: %+v", out)
		}
	})

	t.Run("reads every column and sheet of a two-language workbook", func(t *testing.T) {
		wb := excelize.NewFile()
		defer wb.Close()
		for cell, v := range map[string]string{"A1": "en", "B1": "de", "A2": "Hello", "B2": "Hallo"} {
			if err := wb.SetCellValue("Sheet1", cell, v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := wb.NewSheet("Notes"); err != nil {
			t.Fatal(err)
		}
		if err := wb.SetCellValue("Notes", "A1", "Keep it short"); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := wb.Write(&buf); err != nil {
			t.Fatal(err)
		}
		var out uploadTextOut
		if isErr, text := read(stage("glossary.xlsx", buf.Bytes()), &out); isErr {
			t.Fatalf("read_uploaded_file: %s", text)
		}
		for _, want := range []string{"Hello", "Hallo", "Keep it short"} {
			if !strings.Contains(out.Text, want) {
				t.Errorf("text lacks %q: %q", want, out.Text)
			}
		}
	})

	t.Run("gives an XLIFF's translations next to their source", func(t *testing.T) {
		xliff := `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="ui">
    <body>
      <trans-unit id="1"><source>Save</source><target>Speichern</target></trans-unit>
      <trans-unit id="2"><source>Cancel</source></trans-unit>
    </body>
  </file>
</xliff>`
		var out uploadTextOut
		if isErr, text := read(stage("ui.xliff", []byte(xliff)), &out); isErr {
			t.Fatalf("read_uploaded_file: %s", text)
		}
		if !strings.Contains(out.Text, "Save\tSpeichern\n") || !strings.Contains(out.Text, "Cancel\n") {
			t.Errorf("text = %q, want each translation after its source", out.Text)
		}
	})

	t.Run("refuses another user's, another tenant's and a made-up upload alike", func(t *testing.T) {
		colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "reader-colleague@test.com", "pass", "owner")
		for _, id := range []string{
			stageUpload(t, f, store, f.tenantA.ID, colleague.ID, "theirs.txt", []byte("Private\n")),
			stageUpload(t, f, store, f.tenantB.ID, f.ownerB.ID, "other-tenant.txt", []byte("Private\n")),
			uuid.NewString(),
		} {
			isErr, text := read(id, nil)
			if !isErr {
				t.Errorf("read upload %s", id)
				continue
			}
			assertOpaqueNotFound(t, text)
			if strings.Contains(text, "Private") {
				t.Errorf("the refusal carries the file's text: %s", text)
			}
		}
	})

	t.Run("refuses a format it cannot read", func(t *testing.T) {
		isErr, text := read(stage("logo.png", []byte{0x89, 'P', 'N', 'G'}), nil)
		if !isErr || !strings.Contains(text, "logo.png") || !strings.Contains(text, "cannot read") {
			t.Errorf("png: %v %s", isErr, text)
		}
	})

	t.Run("refuses text over 1 MB", func(t *testing.T) {
		big := strings.Repeat("A sentence of text.\n", (1<<20)/20+10)
		isErr, text := read(stage("huge.txt", []byte(big)), nil)
		if !isErr || !strings.Contains(text, "1 MB") {
			t.Errorf("huge text: %v %.200s", isErr, text)
		}
	})
}
