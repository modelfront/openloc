package segmentation

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

// releasedBuiltins pins every released built-in file. A released set never
// changes: its files are the rules existing files were imported with. To
// improve a set, add a file with the next version (builtin:en@2) and pin it
// here.
var releasedBuiltins = map[string]string{
	"conservative@1.json": "58b1604e482e74d9e532cc427181e16e61276e2907f17982e6e0aec21f715e64",
	"default@1.json":      "12f1df171233455ac4f8867e73dbc799b9237f462c781d987e4271704623cc3d",
	"de@1.json":           "535acb87f81d3d561e1c6ef399fa2e1836838aa36284158af4d471459695f6ed",
	"en@1.json":           "3ca7373aa4542bb8b0cba98d84a5adba43cd11de31b2d8c431562af547575de7",
	"es@1.json":           "f1841c8b3028000469f73df8537fc2906d87fd9d33356c4ec9eb94120cfab036",
	"fr@1.json":           "e5acb2abf2ada10b598f9f0350cd6fcea924800b8311b21723f424bd95701f20",
	"it@1.json":           "7150f814d46de630d2eaa0d235743d72f27234dc2aba9f071e59236e34bd0e4c",
	"pt@1.json":           "a8128d4b5ebcf45d15af70c15d2e92f1af27b643a527274e421f5889f0d77afb",
	"ru@1.json":           "d84e4d2dc0cb52e8aca2f9c2b85d020ceb78d9713cc0758b7684482ed2c98025",
	"th@1.json":           "6240ed3369682ccf86352586a2e133d135029a546d5da0eea51ee9c0a998b221",
}

func TestBuiltins_ReleasedFilesNeverChange(t *testing.T) {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		raw, err := fs.ReadFile(builtinFS, "builtin/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		want, ok := releasedBuiltins[e.Name()]
		if !ok {
			t.Errorf("%s is not pinned in releasedBuiltins (hash %s)", e.Name(), hex.EncodeToString(sum[:]))
			continue
		}
		seen[e.Name()] = true
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s changed (hash %s): a released built-in never changes; add a new version instead", e.Name(), got)
		}
	}
	for name := range releasedBuiltins {
		if !seen[name] {
			t.Errorf("released built-in %s is missing", name)
		}
	}
}

func TestBuiltins_Catalog(t *testing.T) {
	all := Builtins()
	if all[0].ID != DefaultID || all[1].ID != ConservativeID {
		t.Errorf("first sets = %s, %s; want Default, then Conservative", all[0].ID, all[1].ID)
	}
	for _, b := range all {
		if !strings.HasPrefix(b.ID, builtinPrefix) || b.Name == "" || b.Description == "" {
			t.Errorf("%+v lacks an id, a name or a description", b)
		}
		if _, err := Compile(b.RuleSet); err != nil {
			t.Errorf("%s: %v", b.ID, err)
		}
	}
	if _, ok := BuiltinByID("builtin:en@9"); ok {
		t.Error("BuiltinByID found a version that does not exist")
	}
}

func TestBuiltins_Behaviour(t *testing.T) {
	tests := []struct {
		id, text string
		want     []string
	}{
		{"builtin:en@1", "Dr. Smith came at 5 p.m. He left.", []string{"Dr. Smith came at 5 p.m. He left."}},
		{"builtin:en@1", "We met Mr. Brown. Then we left.", []string{"We met Mr. Brown.", "Then we left."}},
		{"builtin:en@1", "See Fig. 3 and No. 12 for details. Next.", []string{"See Fig. 3 and No. 12 for details.", "Next."}},
		{"builtin:en@1", "Revised Information: F7m Specifications", []string{"Revised Information: F7m Specifications"}},
		{"builtin:en@1", "He said \"Hi.\" Then he left. It was OK. Fine.", []string{"He said \"Hi.\"", "Then he left.", "It was OK.", "Fine."}},
		{"builtin:en@1", "Use e.g. Go or i.e. Rust. Done!", []string{"Use e.g. Go or i.e. Rust.", "Done!"}},
		{"builtin:en@1", "It happened in 2020. Then it ended.", []string{"It happened in 2020.", "Then it ended."}},
		{"builtin:de@1", "Am 3. Oktober kam er z.B. an. Dann ging er.", []string{"Am 3. Oktober kam er z.B. an.", "Dann ging er."}},
		{"builtin:de@1", "Installieren Sie die App. Melden Sie sich an.", []string{"Installieren Sie die App.", "Melden Sie sich an."}},
		{"builtin:de@1", "Siehe Art. 5 und den Test. Weiter.", []string{"Siehe Art. 5 und den Test.", "Weiter."}},
		{"builtin:en@1", "Neither did I. Plan A. Then we left.", []string{"Neither did I.", "Plan A.", "Then we left."}},
		{"builtin:es@1", "Falta RAM. Compre más.", []string{"Falta RAM.", "Compre más."}},
		{"builtin:fr@1", "Voir M. Dupont. Il arrive.", []string{"Voir M. Dupont.", "Il arrive."}},
		{"builtin:es@1", "Vino el Sr. García. ¿Y tú? Bien.", []string{"Vino el Sr. García.", "¿Y tú?", "Bien."}},
		{"builtin:th@1", "สวัสดีครับ. ผมชื่อ", []string{"สวัสดีครับ. ผมชื่อ"}},
		{DefaultID, "Hello. Мир. 你好。世界。", []string{"Hello.", "Мир.", "你好。", "世界。"}},
		{ConservativeID, "Revised Information: F7m Specifications", []string{"Revised Information:", "F7m Specifications"}},
	}
	for _, tc := range tests {
		b, ok := BuiltinByID(tc.id)
		if !ok {
			t.Fatalf("no built-in %s", tc.id)
		}
		s := MustCompile(b.RuleSet)
		var got []string
		ranges, err := s.Split(tc.text, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range ranges {
			got = append(got, tc.text[r.Start:r.End])
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s split %q\n got %q\nwant %q", tc.id, tc.text, got, tc.want)
		}
	}
}

func TestBuiltins_WordsHaveNoTrailingFullStop(t *testing.T) {
	for _, b := range Builtins() {
		for _, w := range append(append([]string{}, b.RuleSet.Abbreviations...), b.RuleSet.NumberAbbreviations...) {
			if strings.HasSuffix(w, ".") || strings.TrimSpace(w) != w || w == "" {
				t.Errorf("%s: word %q", b.ID, w)
			}
		}
	}
}
