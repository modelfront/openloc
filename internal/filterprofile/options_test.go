package filterprofile

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"openloc/internal/httputil"
	"openloc/internal/parser"
)

func TestNormalized_XLSXTargetColumns(t *testing.T) {
	got := normalized(FormatXLSX, Options{XLSX: &XLSXRules{
		Columns:       []string{" b "},
		TargetColumns: []parser.XLSXTargetColumn{{Language: " FR ", Column: "c"}, {Language: "pt_BR", Column: "d"}, {}},
	}}).XLSX
	want := []parser.XLSXTargetColumn{{Language: "fr", Column: "C"}, {Language: "pt-br", Column: "D"}}
	if !reflect.DeepEqual(got.TargetColumns, want) || !reflect.DeepEqual(got.Columns, []string{"B"}) {
		t.Errorf("rules = %+v, want source [B] and targets %v", got, want)
	}
	if empty := normalized(FormatXLSX, Options{}).XLSX.TargetColumns; empty == nil || len(empty) != 0 {
		t.Errorf("no rules: target_columns = %#v, want an empty list", empty)
	}
	if got.Empty() {
		t.Error("rules with target columns read as empty")
	}
	if (XLSXRules{TargetColumns: want}).Empty() {
		t.Error("target columns alone read as empty: the upload would ask instead of refusing them")
	}
}

func TestValidate_XLSXTargetColumns(t *testing.T) {
	rules := func(columns []string, targets ...parser.XLSXTargetColumn) Options {
		return normalized(FormatXLSX, Options{XLSX: &XLSXRules{Columns: columns, TargetColumns: targets}})
	}
	fr := parser.XLSXTargetColumn{Language: "fr", Column: "C"}
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"source B, fr in C", rules([]string{"B"}, fr), ""},
		{"no source column", rules(nil, fr), "exactly one source column"},
		{"over the source", rules([]string{"c"}, fr), "Column C holds the source text"},
		{"same column twice", rules([]string{"B"}, fr, parser.XLSXTargetColumn{Language: "de", Column: "c"}), "Column C is the target of both fr and de"},
		{"language twice", rules([]string{"B"}, fr, parser.XLSXTargetColumn{Language: "FR", Column: "D"}), "fr has two target columns"},
		{"not a language", rules([]string{"B"}, parser.XLSXTargetColumn{Language: "qty", Column: "D"}), `"qty" is not a language code`},
		{"not a column", rules([]string{"B"}, parser.XLSXTargetColumn{Language: "de", Column: "D9"}), `"D9" is not a column`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.opts)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			var ce *httputil.ClientError
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate = %v, want a client error with %q", err, tc.want)
			}
		})
	}
}

func TestCheckSizes_XLSXTargetColumns(t *testing.T) {
	o := Options{XLSX: &XLSXRules{TargetColumns: make([]parser.XLSXTargetColumn, parser.MaxXLSXTargetColumns+1)}}
	if err := checkSizes(o); err == nil || !strings.Contains(err.Error(), "At most 100 languages") {
		t.Errorf("checkSizes = %v, want the cap", err)
	}
}
