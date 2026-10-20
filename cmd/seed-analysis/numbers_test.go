package main

import "testing"

// Every seeded file and language has numbers, and each row is a valid
// job_analysis row: the bands add up to the file's words, and no
// auto-approved count is larger than its band (the table's CHECK constraints).
func TestBreakdown_RowsAddUpAndRespectTheTable(t *testing.T) {
	for _, f := range files {
		src, ok := sourceFiles[f.Name]
		if !ok {
			t.Fatalf("%s has no source size", f.Name)
		}
		for _, lang := range targetLangs {
			if _, ok := languages[lang]; !ok {
				t.Fatalf("%s has no leverage", lang)
			}
			p := breakdown(f.Name, lang)
			type col struct{ total, aa int32 }
			bands := map[string][4]col{
				"101":  {{p.Tm101Segments, p.Tm101SegmentsAutoApproved}, {p.Tm101Words, p.Tm101WordsAutoApproved}, {p.Tm101Characters, p.Tm101CharactersAutoApproved}, {p.Tm101WeightedWords, p.Tm101WeightedWordsAutoApproved}},
				"100":  {{p.Tm100Segments, p.Tm100SegmentsAutoApproved}, {p.Tm100Words, p.Tm100WordsAutoApproved}, {p.Tm100Characters, p.Tm100CharactersAutoApproved}, {p.Tm100WeightedWords, p.Tm100WeightedWordsAutoApproved}},
				"8599": {{p.Tm8599Segments, p.Tm8599SegmentsAutoApproved}, {p.Tm8599Words, p.Tm8599WordsAutoApproved}, {p.Tm8599Characters, p.Tm8599CharactersAutoApproved}, {p.Tm8599WeightedWords, p.Tm8599WeightedWordsAutoApproved}},
				"6584": {{p.Tm6584Segments, p.Tm6584SegmentsAutoApproved}, {p.Tm6584Words, p.Tm6584WordsAutoApproved}, {p.Tm6584Characters, p.Tm6584CharactersAutoApproved}, {p.Tm6584WeightedWords, p.Tm6584WeightedWordsAutoApproved}},
				"new":  {{p.NewSegments, p.NewSegmentsAutoApproved}, {p.NewWords, p.NewWordsAutoApproved}, {p.NewCharacters, p.NewCharactersAutoApproved}, {p.NewWeightedWords, p.NewWeightedWordsAutoApproved}},
				"reps": {{p.RepetitionsSegments, p.RepetitionsSegmentsAutoApproved}, {p.RepetitionsWords, p.RepetitionsWordsAutoApproved}, {p.RepetitionsCharacters, p.RepetitionsCharactersAutoApproved}, {p.RepetitionsWeightedWords, p.RepetitionsWeightedWordsAutoApproved}},
			}
			var words int32
			for name, cols := range bands {
				words += cols[1].total
				for _, c := range cols {
					if c.total < 0 || c.aa < 0 || c.aa > c.total {
						t.Errorf("%s/%s band %s: auto-approved %d of %d", f.Name, lang, name, c.aa, c.total)
					}
				}
			}
			if int(words) != src.words {
				t.Errorf("%s/%s: bands hold %d words, want %d", f.Name, lang, words, src.words)
			}
			if p.NewWords <= 0 {
				t.Errorf("%s/%s: no new words", f.Name, lang)
			}
		}
	}
}

// German, with the older TM, leverages more than Japanese on the same file.
func TestBreakdown_LeverageFollowsTheLanguage(t *testing.T) {
	de, ja := breakdown("product-manual.txt", "de"), breakdown("product-manual.txt", "ja")
	if de.NewWords >= ja.NewWords {
		t.Errorf("new words: de %d, ja %d; want de < ja", de.NewWords, ja.NewWords)
	}
	if de.Tm100Words <= ja.Tm100Words {
		t.Errorf("100%% words: de %d, ja %d; want de > ja", de.Tm100Words, ja.Tm100Words)
	}
}
