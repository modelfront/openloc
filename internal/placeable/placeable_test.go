package placeable

import (
	"strings"
	"testing"
)

func render(ps []Placeable) string {
	var b []string
	for _, p := range ps {
		b = append(b, string(p.Kind)+":"+p.Text)
	}
	return strings.Join(b, " | ")
}

func TestFind(t *testing.T) {
	cases := []struct {
		name, text, lang, want string
	}{
		{"number with separators", "Total 1,234.56 items", "en", "number:1,234.56"},
		{"number at sentence end", "It weighs 5.", "en", "number:5"},
		{"german separators", "Summe 1.234,5 Stück", "de", "number:1.234,5"},
		{"date and time", "Due 2024-03-14 at 10:30.", "en", "date:2024-03-14 | time:10:30"},
		{"measurement", "Max 25 kg per box", "en", "measurement:25 kg"},
		{"currency before", "Only $49.90 today", "en", "currency:$49.90"},
		{"currency after with nbsp", "Nur 49,90\u00a0€ heute", "de", "currency:49,90\u00a0€"},
		{"currency code", "Pay EUR 12 now", "en", "currency:EUR 12"},
		{"url trims sentence dot", "Visit https://example.com/a?b=1.", "en", "url:https://example.com/a?b=1"},
		{"www url", "See www.example.com, then go", "en", "url:www.example.com"},
		{"email", "Write to test@test.com today", "en", "email:test@test.com"},
		{"variables", "Hello {name}, you have {{count}} new %s and %1$d ${x}", "en", "variable:{name} | variable:{{count}} | variable:%s | variable:%1$d | variable:${x}"},
		{"code beats number inside it", "Part XJ-500B fits", "en", "code:XJ-500B"},
		{"code with underscore", "Order SKU_1234-B now", "en", "code:SKU_1234-B"},
		{"digit inside a word is no placeable", "the mp3 file", "en", ""},
		{"plain words", "Nothing to see here.", "en", ""},
		{"digits in url are not numbers", "Go to https://example.com/v2/42 now", "en", "url:https://example.com/v2/42"},
		{"leading text with multibyte", "Prix: 3 € – voir {0}", "fr", "currency:3 € | variable:{0}"},
		{"percent before a word is no variable", "Get 20% discount, 100% sure", "en", "number:20 | number:100"},
		{"spaced percent is no variable", "50 % de remise", "fr", "number:50"},
		{"a percent glued to a word is no variable", "eine 20%-ige Erhöhung, 100%ige Garantie, Get 50%-discount", "de", "number:20 | number:100 | number:50"},
		{"a printf variable after a letter or space stays", "Wait %ds, size %dx%d", "en", "variable:%d | variable:%d | variable:%d"},
		{"prefixed dollar", "Pay US$ 5 now", "en", "currency:US$ 5"},
		{"a number glued to letters is no placeable", "Get 5GB on a 12V line, 2 500GB, in 2024 500GB", "en", "number:2 | number:2024"},
		{"number before CJK text", "価格は500円です", "ja", "number:500"},
		{"ordinals are no placeables", "the 1st and 22nd floor", "en", ""},
		{"a code starts with a letter", "Watch 4K with 2FA on 5G, model A4", "en", "code:A4"},
		{"dollar prefix inside a word", "PLUS$5 deal", "en", "currency:$5"},
		{"code next to CJK text", "型番ABC123です", "ja", "code:ABC123"},
		{"decades and plurals are no measurements", "the 1990s, 80s and iPhone 5s", "en", ""},
		{"million after a currency is no measurement", "raised $5m", "en", ""},
		{"number before Thai text", "ราคา500บาท", "th", "number:500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := render(Find(c.text, c.lang))
			if got != c.want {
				t.Errorf("Find(%q)\n got: %s\nwant: %s", c.text, got, c.want)
			}
		})
	}
}

func TestFind_OffsetsMatchText(t *testing.T) {
	s := "Ünïcödé {0} costs €5 at 10:30"
	for _, p := range Find(s, "en") {
		if s[p.Start:p.End] != p.Text {
			t.Errorf("offsets [%d,%d) give %q, text is %q", p.Start, p.End, s[p.Start:p.End], p.Text)
		}
	}
}

func TestCount(t *testing.T) {
	cases := []struct {
		hay  string
		p    Placeable
		want int
	}{
		{"Größe %dx%d", Placeable{Kind: KindVariable, Text: "%d"}, 2},
		{"den MP3-Player", Placeable{Kind: KindCode, Text: "MP3"}, 1},
		{"XJ-500B-2", Placeable{Kind: KindCode, Text: "XJ-500B"}, 0},
		{"SKU_1234_B", Placeable{Kind: KindCode, Text: "SKU_1234"}, 0},
		{"es sind 15", Placeable{Kind: KindNumber, Text: "5"}, 0},
		{"es sind 5.", Placeable{Kind: KindNumber, Text: "5"}, 1},
		{"PLUS$5 Angebot", Placeable{Kind: KindCurrency, Text: "$5"}, 1},
		{"価格は500円です", Placeable{Kind: KindNumber, Text: "500"}, 1},
		{"x²5", Placeable{Kind: KindNumber, Text: "5"}, 1},
		{"رقمXJ500 جاهز", Placeable{Kind: KindCode, Text: "XJ500"}, 1},
	}
	for _, c := range cases {
		if got := Count(c.hay, c.p); got != c.want {
			t.Errorf("Count(%q, %q) = %d, want %d", c.hay, c.p.Text, got, c.want)
		}
	}
}
