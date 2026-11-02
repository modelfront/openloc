package numeric

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

// A value written the target language's way is always the same value: for
// any number, and any target language, no check of the group objects.
func TestRun_LocalizedValuesAlwaysMatch(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	en := localeFor("en")
	r := allChecks
	r.FormatRequire = true
	for _, lang := range []string{"de", "fr", "es", "it", "pt-br", "ru", "pl", "sv", "nl", "de-ch", "ja", "zh-hans", "ko", "tr", "en-gb"} {
		tl := localeFor(lang)
		for range 200 {
			v := fmt.Sprintf("%d.%02d", rng.IntN(10_000_000), rng.IntN(99)+1)
			src := "Total " + localized(v, en) + " today"
			tgt := "Gesamt " + localized(v, tl) + " heute"
			if got := Run(src, tgt, "en", lang, r); len(got) != 0 {
				t.Fatalf("%s: %q → %q: want no findings, got %+v", lang, src, tgt, got)
			}
		}
	}
}

func FuzzRun(f *testing.F) {
	for _, s := range []string{
		"Price: 25.16 EUR", "Check-in from 3:30 PM.", "Offer valid until March 14, 2026.",
		"The pool is 82 feet long.", "Pages 10-20", "Version 1.2.3", "{0} items", "６'１\"", "14.3.",
		"1 000,5", "−5 °C", "２０２６年３月１４日", "12:60", "99/99/9999", "9999999999999999999999 km",
	} {
		f.Add(s, "en", "de")
	}
	f.Fuzz(func(t *testing.T, s, sl, tl string) {
		a := Run(s, s, sl, sl, allChecks)
		if len(a) != 0 {
			t.Fatalf("identical text %q (%s): want no findings, got %+v", s, sl, a)
		}
		b1 := Run(s, s+" 7", sl, tl, allChecks)
		b2 := Run(s, s+" 7", sl, tl, allChecks)
		if !reflect.DeepEqual(b1, b2) {
			t.Fatalf("not deterministic for %q: %+v vs %+v", s, b1, b2)
		}
	})
}
