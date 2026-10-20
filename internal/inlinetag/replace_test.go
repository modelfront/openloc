package inlinetag

import "testing"

func TestReplaceText(t *testing.T) {
	cases := []struct {
		name, text, find, repl string
		cs                     bool
		n                      int
		want                   string
	}{
		{"case sensitive", "Hello hello Hello", "Hello", "Hi", true, 2, "Hi hello Hi"},
		{"case insensitive keeps surroundings", "Hello HELLO there", "hello", "hi", false, 2, "hi hi there"},
		{"İ before the match", "İstanbul abc def", "ABC", "x", false, 1, "İstanbul x def"},
		{"multibyte match", "Grüße GRÜSSE grüße", "GRÜßE", "x", false, 2, "x GRÜSSE x"},
		{"no match keeps text", "abc", "z", "y", false, 0, "abc"},
		{"empty find", "abc", "", "y", false, 0, "abc"},
		{"non-overlapping", "aaaa", "aa", "b", true, 2, "bb"},
		{"dotted capital I before the match", "İstanbul ve Ankara", "ankara", "İzmir", false, 1, "İstanbul ve İzmir"},
		{"letter that grows when lowercased", "Ⱥ abc ABC", "abc", "x", false, 2, "Ⱥ x x"},
		{"Kelvin sign before the match", "K abc", "abc", "x", false, 1, "K x"},
		{"replacement taken literally", "price abc", "ABC", "$1", false, 1, "price $1"},
	}
	for _, c := range cases {
		n, got := ReplaceText(c.text, c.find, c.repl, c.cs)
		if n != c.n || got != c.want {
			t.Errorf("%s: got (%d, %q), want (%d, %q)", c.name, n, got, c.n, c.want)
		}
	}
}

func TestReplaceInRich(t *testing.T) {
	cases := []struct {
		name, rich, find, repl string
		n                      int
		want                   string
	}{
		{"text around a paired tag", `Click <g id="1">Save</g> now`, "now", "today", 1, `Click <g id="1">Save</g> today`},
		{"inside a paired tag", `Click <g id="1">Save</g> now`, "Save", "Store", 1, `Click <g id="1">Store</g> now`},
		{"straddling a tag is not replaced", `<g id="1">Sa</g>ve now`, "Save", "Store", 0, `<g id="1">Sa</g>ve now`},
		{"placeholder content untouched", `Order <ph id="4">{order}</ph> order`, "order", "pedido", 1, `Order <ph id="4">{order}</ph> pedido`},
		{"attributes untouched", `<x id="now"/> now`, "now", "today", 1, `<x id="now"/> today`},
		{"bare < stays one run", `a < b`, "a < b", "c", 1, `c`},
	}
	for _, c := range cases {
		n, got := ReplaceInRich(c.rich, c.find, c.repl, true)
		if n != c.n || got != c.want {
			t.Errorf("%s: got (%d, %q), want (%d, %q)", c.name, n, got, c.n, c.want)
		}
	}
}
