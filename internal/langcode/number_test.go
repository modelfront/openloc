package langcode

import "testing"

func TestDecimalSeparator(t *testing.T) {
	tests := []struct {
		code string
		want rune
	}{
		{"en", '.'},
		{"en-us", '.'},
		{"ja", '.'},
		{"zh-hans", '.'},
		{"de", ','},
		{"de-de", ','},
		{"fr-fr", ','},
		{"pt-br", ','},
		{"ru", ','},
		// The region decides when it differs from the language's default.
		{"de-ch", '.'},
		{"es", ','},
		{"es-mx", '.'},
		// Egyptian Arabic and Persian write their own digits by default.
		{"ar", '.'},
		{"ar-eg", 0},
		{"fa", 0},
		{"", 0},
		{"not a code", 0},
	}
	for _, tt := range tests {
		if got := DecimalSeparator(tt.code); got != tt.want {
			t.Errorf("DecimalSeparator(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

// Every locale of the table reads numeric dates in one of the three orders
// the date check knows, with Latin separators where its digits are Latin.
func TestCLDRTable_IsUsable(t *testing.T) {
	if len(cldrLocales) < 100 {
		t.Fatalf("the table has %d locales", len(cldrLocales))
	}
	for code, l := range cldrLocales {
		switch l.DateOrder {
		case "mdy", "dmy", "ymd":
		default:
			t.Errorf("%s: date order %q", code, l.DateOrder)
		}
		if l.Group == 0 {
			t.Errorf("%s: no grouping separator", code)
		}
		if !l.NativeDigits && l.Decimal != '.' && l.Decimal != ',' {
			t.Errorf("%s: Latin digits with decimal separator %q", code, l.Decimal)
		}
	}
}
