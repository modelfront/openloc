package xlsx

import (
	"context"
	"errors"
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xlsx/xlsxtest"
	"strings"
	"testing"
)

// A workbook with several target columns exports once per job, and each
// export must write only its own language's column.
//
// Each language is exported many times: a column choice that depends on map
// iteration order passes a single run by luck, never all of them.
func TestXLSXReconstruct_MultiTargetWritesJobLanguageColumn(t *testing.T) {
	original := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "de", "it"},
		[][]string{
			{"Hello", "Bonjour", "Hallo", "Ciao"},
		},
	)

	segments, err := XLSX{}.Parse(original)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segments) != 1 || segments[0].Key != "row_2" {
		t.Fatalf("segments = %+v, want one segment keyed row_2", segments)
	}
	key := segments[0].Key

	originalCells := map[string]string{"B2": "Bonjour", "C2": "Hallo", "D2": "Ciao"}
	jobs := []struct {
		lang, cell, translation string
	}{
		{"fr", "B2", "Salut"},
		{"de", "C2", "Servus"},
		{"it", "D2", "Salve"},
	}

	const runs = 20
	for _, job := range jobs {
		t.Run(job.lang, func(t *testing.T) {
			for run := 0; run < runs; run++ {
				out, err := exportXLSX(t, original, map[string]string{key: job.translation}, job.lang)
				if err != nil {
					t.Fatalf("export: %v", err)
				}
				got := xlsxtest.ReadCells(t, out, "B2", "C2", "D2")
				for cell, w := range originalCells {
					if cell == job.cell {
						w = job.translation
					}
					if got[cell] != w {
						t.Fatalf("run %d: %s export: %s = %q, want %q (row 2 after export: B=%q C=%q D=%q)",
							run+1, job.lang, cell, got[cell], w, got["B2"], got["C2"], got["D2"])
					}
				}
			}
		})
	}
}

// Without a language, a workbook with several target columns has no right
// column to write, so Reconstruct fails instead of picking one.
func TestXLSXReconstruct_MultiTargetWithoutLanguageFails(t *testing.T) {
	original := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "de", "it"},
		[][]string{
			{"Hello", "Bonjour", "Hallo", "Ciao"},
		},
	)

	out, err := XLSX{}.Reconstruct(original, map[string]string{"row_2": "Servus"})
	if err == nil {
		t.Fatalf("Reconstruct wrote a workbook with no language (row 2: %v), want an error", xlsxtest.ReadCells(t, out, "B2", "C2", "D2"))
	}
	if !strings.Contains(err.Error(), "target language is required") {
		t.Errorf("err = %v, want it to say the target language is required", err)
	}
}

func TestXLSXReconstructForLang_ColumnChoice(t *testing.T) {
	tests := []struct {
		name     string
		header   []string
		lang     string
		wantCell string // cell that receives the translation; empty when an error is expected
		wantErr  string
	}{
		{name: "exact code, any case", header: []string{"en", "fr", "de"}, lang: "DE", wantCell: "C2"},
		{name: "region finds the base column", header: []string{"en", "fr", "de"}, lang: "de-AT", wantCell: "C2"},
		{name: "base finds the region column", header: []string{"en", "fr-FR", "de"}, lang: "fr", wantCell: "B2"},
		{name: "no language codes: column B", header: []string{"Content"}, lang: "de", wantCell: "B2"},
		{name: "no column for the language", header: []string{"en", "fr", "de"}, lang: "es", wantErr: `No target column for language "es"`},
		{name: "the only target column is another language", header: []string{"en", "fr"}, lang: "de", wantErr: `No target column for language "de"`},
		{name: "a sibling variant is not the job's language", header: []string{"en", "zh-Hans"}, lang: "zh-Hant", wantErr: `No target column for language "zh-Hant"`},
		{name: "two columns share the base", header: []string{"en", "fr-FR", "fr-CA"}, lang: "fr", wantErr: "matches several target columns (fr-ca, fr-fr)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := []string{"Hello", "x1", "x2"}[:max(len(tc.header), 1)]
			original := xlsxtest.BuildXLSX(t, tc.header, [][]string{row})

			out, err := exportXLSX(t, original, map[string]string{"row_2": "T"}, tc.lang)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				// Handlers show this message to the user, so it must be typed.
				var ee *common.ExportError
				if !errors.As(err, &ee) {
					t.Fatalf("err %v must be an *ExportError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			got := xlsxtest.ReadCells(t, out, "B2", "C2")
			want := map[string]string{"B2": "", "C2": ""}
			for i, v := range row[1:] {
				want[string(rune('B'+i))+"2"] = v
			}
			want[tc.wantCell] = "T"
			for cell, w := range want {
				if got[cell] != w {
					t.Errorf("%s = %q, want %q (row 2 after export: B=%q C=%q)", cell, got[cell], w, got["B2"], got["C2"])
				}
			}
		})
	}
}

// Row 1 with one language code next to other headings is a multi-language
// sheet whose other columns use codes the parser does not read (es-419,
// pt_BR). The export stops with a message instead of writing column B, which
// holds another language's text.
func TestXLSXReconstructForLang_OneLanguageCodeAmongHeadingsFails(t *testing.T) {
	original := xlsxtest.BuildXLSX(t,
		[]string{"en", "es-419", "pt_BR"},
		[][]string{{"Hello", "Hola", "Olá"}},
	)
	segments, err := XLSX{}.Parse(original)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	translations := make(map[string]string, len(segments))
	for _, seg := range segments {
		translations[seg.Key] = "PT:" + seg.Text
	}

	for _, lang := range []string{"pt-BR", "es-419", ""} {
		t.Run("lang="+lang, func(t *testing.T) {
			out, err := exportXLSX(t, original, translations, lang)
			if err == nil {
				got := xlsxtest.ReadCells(t, out, "B1", "B2", "C2")
				t.Fatalf("export succeeded and wrote B1=%q B2=%q C2=%q; want an *ExportError", got["B1"], got["B2"], got["C2"])
			}
			var ee *common.ExportError
			if !errors.As(err, &ee) {
				t.Fatalf("err %v must be an *ExportError", err)
			}
			// The message reaches the user and must not quote the file.
			for _, header := range []string{"es-419", "pt_BR"} {
				if strings.Contains(ee.Msg, header) {
					t.Errorf("message %q quotes the header cell %q", ee.Msg, header)
				}
			}
		})
	}
}

// A one-column sheet whose first cell is a short word ("OK") is still a
// single-column sheet: row 1 is content and its translation goes to B1.
func TestXLSXReconstructForLang_OneColumnShortFirstCellExports(t *testing.T) {
	original := xlsxtest.BuildXLSX(t, []string{"OK"}, [][]string{{"Cancel"}})
	out, err := exportXLSX(t, original, map[string]string{"row_1": "D'accord", "row_2": "Annuler"}, "fr")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	got := xlsxtest.ReadCells(t, out, "B1", "B2")
	if got["B1"] != "D'accord" || got["B2"] != "Annuler" {
		t.Errorf("B1=%q B2=%q, want the translations of rows 1 and 2", got["B1"], got["B2"])
	}
}

func exportXLSX(t *testing.T, original []byte, translations map[string]string, targetLang string) ([]byte, error) {
	t.Helper()
	return XLSX{}.ReconstructForLang(context.Background(), original, translations, common.ExportLangs{Target: targetLang})
}
