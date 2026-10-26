package xlsx

import (
	"bytes"
	"context"
	"encoding/json"
	"openloc/internal/parsers/xlsx/xlsxtest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"openloc/internal/parsers/common"
)

func TestXLSXParse_TwoColumns(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr"},
		[][]string{
			{"Hello", "Bonjour"},
			{"Goodbye", "Au revoir"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	if segments[0].Key != "row_2" {
		t.Errorf("seg[0].Key = %q, want row_2", segments[0].Key)
	}
	if segments[0].Text != "Hello" {
		t.Errorf("seg[0].Text = %q, want Hello", segments[0].Text)
	}
	if segments[1].Text != "Goodbye" {
		t.Errorf("seg[1].Text = %q, want Goodbye", segments[1].Text)
	}
	// Target text is imported per language by ExtractXLSXTargets, even from a
	// single target column, so Parse leaves Target empty.
	if segments[0].Target != "" || segments[1].Target != "" {
		t.Errorf("Parse set Target: %q, %q", segments[0].Target, segments[1].Target)
	}
	targets, err := ExtractXLSXTargets(content, []string{"fr"})
	if err != nil {
		t.Fatalf("ExtractXLSXTargets: %v", err)
	}
	if targets["fr"]["row_2"].Text != "Bonjour" || targets["fr"]["row_3"].Text != "Au revoir" {
		t.Errorf("fr targets = %+v", targets["fr"])
	}
}

func TestXLSXParse_MultiTarget(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "de"},
		[][]string{
			{"Hello", "Bonjour", "Hallo"},
			{"Goodbye", "Au revoir", "Auf Wiedersehen"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	// Multi-target: Target should be empty (handled by ExtractXLSXTargets)
	if segments[0].Target != "" {
		t.Errorf("seg[0].Target should be empty for multi-target, got %q", segments[0].Target)
	}

	// Context should contain metadata
	var meta xlsxMeta
	if err := json.Unmarshal([]byte(segments[0].Context), &meta); err != nil {
		t.Fatalf("parse context: %v", err)
	}
	if meta.SourceCol != 0 {
		t.Errorf("SourceCol = %d, want 0", meta.SourceCol)
	}
	if len(meta.TargetCols) != 2 {
		t.Errorf("TargetCols count = %d, want 2", len(meta.TargetCols))
	}
	if meta.TargetCols["fr"] != 1 {
		t.Errorf("TargetCols[fr] = %d, want 1", meta.TargetCols["fr"])
	}
	if meta.TargetCols["de"] != 2 {
		t.Errorf("TargetCols[de] = %d, want 2", meta.TargetCols["de"])
	}
}

func TestXLSXParse_SkipsEmptyRows(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr"},
		[][]string{
			{"Hello", "Bonjour"},
			{"", ""},
			{"Goodbye", "Au revoir"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments (empty row skipped), got %d", len(segments))
	}
	if segments[0].Key != "row_2" {
		t.Errorf("first seg key = %q, want row_2", segments[0].Key)
	}
	if segments[1].Key != "row_4" {
		t.Errorf("second seg key = %q, want row_4 (row 3 was empty)", segments[1].Key)
	}
}

func TestXLSXParse_BCP47Headers(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"EN-US", "pt-BR", "zh-Hans"},
		[][]string{
			{"Hello", "Olá", "你好"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	var meta xlsxMeta
	json.Unmarshal([]byte(segments[0].Context), &meta)
	if _, ok := meta.TargetCols["pt-br"]; !ok {
		t.Errorf("expected pt-br in target cols, got %v", meta.TargetCols)
	}
	if _, ok := meta.TargetCols["zh-hans"]; !ok {
		t.Errorf("expected zh-hans in target cols, got %v", meta.TargetCols)
	}
}

func TestXLSXParse_TooFewLangColumns(t *testing.T) {
	// Two columns but only 1 is a language code AND the other isn't "Source" → error
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "Notes"}, // "Notes" is not a lang code, not "Source"
		[][]string{
			{"Hello", "Some note"},
		},
	)

	p := XLSX{}
	// Not bilingual, so every text cell is a segment, the header included.
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("expected the cell mode, got error: %v", err)
	}
	var texts []string
	for _, s := range segments {
		texts = append(texts, s.Text)
	}
	if want := []string{"en", "Notes", "Hello", "Some note"}; strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("segments = %q, want %q", texts, want)
	}
}

// Pins that a single-column xlsx is monolingual: every non-empty row is one
// segment.
func TestXLSXParse_MonolingualSingleColumn(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"esUS _Localization"},
		[][]string{
			{"CONVERSATION ID: vdlg1_001"},
			{"-----------------------"},
			{"USER:   ¡Hola! ¿Cómo te va?"},
			{"AGENT:  Bastante bien. ¿Y tú qué tal?"},
			{""}, // empty row — skipped
			{"USER:   Estoy un poco ocupado."},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("monolingual xlsx should parse, got: %v", err)
	}
	// Every text cell, row 1 included; the empty row and the row of dashes
	// (no letter) are not segments.
	if len(segments) != 5 {
		t.Fatalf("expected 5 segments, got %d", len(segments))
	}
	if segments[0].Text != "esUS _Localization" {
		t.Errorf("seg[0] = %q, want header row", segments[0].Text)
	}
	if segments[1].Text != "CONVERSATION ID: vdlg1_001" {
		t.Errorf("seg[1] = %q, want conversation ID line", segments[1].Text)
	}
	if segments[2].Text != "USER:   ¡Hola! ¿Cómo te va?" {
		t.Errorf("seg[2] = %q, want Hola line", segments[2].Text)
	}
}

func TestXLSXParse_MonolingualWithHeader(t *testing.T) {
	// Column A has a non-language header like "Source Text", "Content", etc.
	content := xlsxtest.BuildXLSX(t,
		[]string{"Content"},
		[][]string{
			{"Hello world"},
			{"Reset password"},
			{"Thank you"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("monolingual xlsx with header should parse, got: %v", err)
	}
	// 4 segments: header "Content" + 3 data rows (monolingual includes all)
	if len(segments) != 4 {
		t.Fatalf("expected 4 segments (header + 3 data), got %d", len(segments))
	}
	if segments[1].Text != "Hello world" {
		t.Errorf("seg[1] = %q, want 'Hello world'", segments[1].Text)
	}
}

func TestXLSXParse_RealQAFile(t *testing.T) {
	// A single-column file whose first row is "esUS _Localization"
	// (not a valid lang code),
	// followed by conversation data.
	content := xlsxtest.BuildXLSX(t,
		[]string{"esUS _Localization"},
		[][]string{
			{"CONVERSATION ID: vdlg1_001_budgeting_app"},
			{"-----------------------"},
			{"USER:   ¡Hola! ¿Cómo te va?"},
			{"AGENT:  Bastante bien."},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("QA expert's xlsx format should parse, got: %v", err)
	}
	if len(segments) < 3 {
		t.Fatalf("expected at least 3 segments, got %d", len(segments))
	}
}

// A stored file segmented by column A carries row keys ("row_N"); its
// translations go to column B, next to the source in column A.
func TestXLSXReconstruct_Monolingual(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"Content"},
		[][]string{
			{"Hello world"},
			{"Thank you"},
		},
	)

	p := XLSX{}
	translations := map[string]string{
		"row_2": "Hola mundo",
		"row_3": "Gracias",
	}
	out, err := p.Reconstruct(content, translations)
	if err != nil {
		t.Fatalf("Reconstruct monolingual: %v", err)
	}

	// Verify column B has translations
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open reconstructed: %v", err)
	}
	defer f.Close()

	val, _ := f.GetCellValue("Sheet1", "B2")
	if val != "Hola mundo" {
		t.Errorf("B2 = %q, want 'Hola mundo'", val)
	}
	val, _ = f.GetCellValue("Sheet1", "B3")
	if val != "Gracias" {
		t.Errorf("B3 = %q, want 'Gracias'", val)
	}
}

// Without language codes, row 1 is content on import and on export: every
// segment comes back translated into its own cell, row 1 included.
func TestXLSXReconstruct_MonolingualRoundTripIncludesRowOne(t *testing.T) {
	content := xlsxtest.BuildXLSX(t, []string{"Welcome"}, [][]string{{"Hello world"}, {"Thank you"}})

	p := XLSX{}
	segs, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	translated := map[string]string{"Welcome": "Bienvenido", "Hello world": "Hola mundo", "Thank you": "Gracias"}
	translations := map[string]string{}
	for _, s := range segs {
		translations[s.Key] = translated[s.Text]
	}

	out, err := p.Reconstruct(content, translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open reconstructed: %v", err)
	}
	defer f.Close()
	for cell, want := range map[string]string{"A1": "Bienvenido", "A2": "Hola mundo", "A3": "Gracias"} {
		if got, _ := f.GetCellValue("Sheet1", cell); got != want {
			t.Errorf("%s = %q, want %q", cell, got, want)
		}
	}
}

// A one-cell sheet is a whole file: it imports as one segment and exports its
// translation into the same cell.
func TestXLSX_MonolingualSingleRow(t *testing.T) {
	content := xlsxtest.BuildXLSX(t, []string{"Only line"}, nil)

	p := XLSX{}
	segs, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse one-row sheet: %v", err)
	}
	if len(segs) != 1 || segs[0].Text != "Only line" {
		t.Fatalf("segments = %+v, want one segment \"Only line\"", segs)
	}

	out, err := p.Reconstruct(content, map[string]string{segs[0].Key: "Única línea"})
	if err != nil {
		t.Fatalf("Reconstruct one-row sheet: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open reconstructed: %v", err)
	}
	defer f.Close()
	if got, _ := f.GetCellValue("Sheet1", "A1"); got != "Única línea" {
		t.Errorf("A1 = %q, want %q", got, "Única línea")
	}
}

func TestXLSXReconstruct_RoundTrip(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr"},
		[][]string{
			{"Hello", ""},
			{"Goodbye", ""},
		},
	)

	p := XLSX{}
	translations := map[string]string{
		"row_2": "Bonjour",
		"row_3": "Au revoir",
	}

	result, err := p.Reconstruct(content, translations)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	// Re-parse the result to verify translations were written
	f, _ := excelize.OpenReader(bytes.NewReader(result))
	defer f.Close()

	val, _ := f.GetCellValue("Sheet1", "B2")
	if val != "Bonjour" {
		t.Errorf("B2 = %q, want Bonjour", val)
	}
	val, _ = f.GetCellValue("Sheet1", "B3")
	if val != "Au revoir" {
		t.Errorf("B3 = %q, want Au revoir", val)
	}

	// Source should be preserved
	val, _ = f.GetCellValue("Sheet1", "A2")
	if val != "Hello" {
		t.Errorf("A2 = %q, want Hello (source preserved)", val)
	}
}

func TestXLSXReconstructForLang(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "de"},
		[][]string{
			{"Hello", "", ""},
			{"Goodbye", "", ""},
		},
	)

	translations := map[string]string{
		"row_2": "Hallo",
		"row_3": "Auf Wiedersehen",
	}

	result, err := XLSX{}.ReconstructForLang(context.Background(), content, translations, common.ExportLangs{Target: "de"})
	if err != nil {
		t.Fatalf("ReconstructForLang: %v", err)
	}

	f, _ := excelize.OpenReader(bytes.NewReader(result))
	defer f.Close()

	// DE column (C) should have translations
	val, _ := f.GetCellValue("Sheet1", "C2")
	if val != "Hallo" {
		t.Errorf("C2 = %q, want Hallo", val)
	}

	// FR column (B) should still be empty
	val, _ = f.GetCellValue("Sheet1", "B2")
	if val != "" {
		t.Errorf("B2 = %q, want empty (FR not exported)", val)
	}
}

func TestExtractXLSXTargets(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "de"},
		[][]string{
			{"Hello", "Bonjour", "Hallo"},
			{"Goodbye", "Au revoir", ""},
		},
	)

	targets, err := ExtractXLSXTargets(content, []string{"fr", "de"})
	if err != nil {
		t.Fatalf("ExtractXLSXTargets: %v", err)
	}

	if targets["fr"]["row_2"].Text != "Bonjour" {
		t.Errorf("fr[row_2] = %q, want Bonjour", targets["fr"]["row_2"].Text)
	}
	if targets["fr"]["row_3"].Text != "Au revoir" {
		t.Errorf("fr[row_3] = %q, want Au revoir", targets["fr"]["row_3"].Text)
	}
	if targets["de"]["row_2"].Text != "Hallo" {
		t.Errorf("de[row_2] = %q, want Hallo", targets["de"]["row_2"].Text)
	}
	// de row_3 was empty — should not be in map
	if _, ok := targets["de"]["row_3"]; ok {
		t.Errorf("de[row_3] should not exist (empty cell)")
	}
}

// Import reads, for each target language, the column the export writes for
// it: a base-code column feeds a regional language and back, a language that
// two columns fit gets neither, and a column no target language reads is
// ignored.
func TestExtractXLSXTargets_SameColumnAsExport(t *testing.T) {
	content := xlsxtest.BuildXLSX(t,
		[]string{"en", "fr", "pt-br", "pt-pt", "ja"},
		[][]string{{"Hello", "Bonjour", "Olá BR", "Olá PT", "こんにちは"}},
	)

	targets, err := ExtractXLSXTargets(content, []string{"fr-ca", "pt", "de"})
	if err != nil {
		t.Fatalf("ExtractXLSXTargets: %v", err)
	}
	if got := targets["fr-ca"]["row_2"].Text; got != "Bonjour" {
		t.Errorf("fr-ca[row_2] = %q, want the fr column", got)
	}
	for _, lang := range []string{"pt", "de", "ja", "fr"} {
		if _, ok := targets[lang]; ok {
			t.Errorf("targets has %q: %v", lang, targets[lang])
		}
	}

	// The export for the same language writes into the column import read.
	out, err := XLSX{}.ReconstructForLang(context.Background(), content, map[string]string{"row_2": "Salut"}, common.ExportLangs{Target: "fr-ca"})
	if err != nil {
		t.Fatalf("ReconstructForLang: %v", err)
	}
	again, err := ExtractXLSXTargets(out, []string{"fr-ca"})
	if err != nil {
		t.Fatalf("ExtractXLSXTargets after export: %v", err)
	}
	if got := again["fr-ca"]["row_2"].Text; got != "Salut" {
		t.Errorf("after export fr-ca[row_2] = %q, want Salut", got)
	}
}

func TestXLSXParse_NewlineInHeader(t *testing.T) {
	// Headers like "English (UK)\nen-GB" — language code is on the second line
	content := xlsxtest.BuildXLSX(t,
		[]string{"English (UK)\nen-GB", "German (Germany)\nde-DE"},
		[][]string{
			{"Hello", "Hallo"},
			{"Goodbye", "Auf Wiedersehen"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	if segments[0].Text != "Hello" {
		t.Errorf("seg[0].Text = %q, want Hello", segments[0].Text)
	}
	targets, err := ExtractXLSXTargets(content, []string{"de-de"})
	if err != nil {
		t.Fatalf("ExtractXLSXTargets: %v", err)
	}
	if got := targets["de-de"]["row_2"].Text; got != "Hallo" {
		t.Errorf("de-de[row_2] = %q, want Hallo", got)
	}
}

func TestXLSXParse_SourceTextColumn(t *testing.T) {
	// "Source text" column is not a language code but contains the source strings.
	// Language columns are all targets.
	content := xlsxtest.BuildXLSX(t,
		[]string{"Context", "Source text", "French (France)\nfr-FR", "German (Germany)\nde-DE"},
		[][]string{
			{"ctx1", "Hello", "Bonjour", "Hallo"},
			{"ctx2", "Goodbye", "Au revoir", "Auf Wiedersehen"},
		},
	)

	p := XLSX{}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	// Source text should come from column B (index 1), not a language column
	if segments[0].Text != "Hello" {
		t.Errorf("seg[0].Text = %q, want Hello", segments[0].Text)
	}

	// Should have target columns for fr and de
	var meta xlsxMeta
	if err := json.Unmarshal([]byte(segments[0].Context), &meta); err != nil {
		t.Fatalf("parse context: %v", err)
	}
	if meta.SourceCol != 1 {
		t.Errorf("SourceCol = %d, want 1 (Source text column)", meta.SourceCol)
	}
	if len(meta.TargetCols) != 2 {
		t.Errorf("TargetCols count = %d, want 2", len(meta.TargetCols))
	}
	if _, ok := meta.TargetCols["fr-fr"]; !ok {
		t.Errorf("expected fr-fr in target cols, got %v", meta.TargetCols)
	}
	if _, ok := meta.TargetCols["de-de"]; !ok {
		t.Errorf("expected de-de in target cols, got %v", meta.TargetCols)
	}
}

func TestFormatFromFilename_XLSX(t *testing.T) {
	tests := []struct {
		name   string
		expect string
	}{
		{"translations.xlsx", "xlsx"},
		{"MY_FILE.XLSX", "xlsx"},
		{"data.csv", ""},
	}
	for _, tc := range tests {
		got := common.FormatFromFilename(tc.name)
		if got != tc.expect {
			t.Errorf("FormatFromFilename(%q) = %q, want %q", tc.name, got, tc.expect)
		}
	}
}
