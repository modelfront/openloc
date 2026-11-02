package numeric

import (
	"slices"
	"sync"

	"openloc/internal/langcode"
)

// locale is what the scanners need to know about a language. An unknown
// language reads numbers and dates every plausible way.
type locale struct {
	known bool
	// decimal is the decimal separator ('.' or ','), 0 when not known.
	decimal, group  rune
	spaceGroup      bool // groups digits with a space
	apostropheGroup bool // groups digits with an apostrophe
	noSpaceGroups   bool // read no space or apostrophe as grouping
	minGrouping     int
	// dateOrders are the field orders the language writes a numeric date in
	// ("mdy", "dmy", "ymd"); other orders are tried only when none of these
	// gives a valid date.
	dateOrders []string
	hour12     bool
	am, pm     []string
	months     [12][]string
	// monthWords are month names that are also everyday words: with no year
	// they count as dates only when they match the other side.
	monthWords []string
}

// monthWords lists, by language, the month names that are also everyday
// words, which CLDR does not mark: Romanian "mai" is May and "more".
var monthWords = map[string][]string{
	"ro": {"mai", "Mai"},
}

var locales sync.Map // code → locale

func localeFor(code string) locale {
	if l, ok := locales.Load(code); ok {
		return l.(locale)
	}
	l := buildLocale(code)
	locales.Store(code, l)
	return l
}

func buildLocale(code string) locale {
	cl, ok := langcode.LocaleFor(code)
	if !ok {
		return locale{dateOrders: allOrders, hour12: true}
	}
	l := locale{
		known:       true,
		decimal:     cl.Decimal,
		group:       cl.Group,
		minGrouping: cl.MinGrouping,
		hour12:      cl.Hour12,
		am:          cl.AM,
		pm:          cl.PM,
		months:      cl.Months,
		monthWords:  monthWords[langcode.BaseLang(langcode.Normalize(code))],
	}
	l.decimal = langcode.DecimalSeparator(code)
	switch normRune(cl.Group) {
	case nbsp, ' ':
		l.spaceGroup = true
	case '\'':
		l.apostropheGroup = true
	}
	l.dateOrders = allOrders
	if slices.Contains(allOrders, cl.DateOrder) {
		l.dateOrders = []string{cl.DateOrder}
	}
	// A bare "en" is written in the US and everywhere else alike: its day and
	// month may come either way round.
	if langcode.Normalize(code) == "en" {
		l.dateOrders = append(l.dateOrders, "dmy")
	}
	return l
}

// with returns the locale with its separators replaced by s, when s is set.
// The digits are grouped from four on, as the separators are named by an
// example such as "1,234.56".
func (l locale) with(s Separators) locale {
	if s.Decimal == 0 {
		return l
	}
	l.known = true
	l.decimal, l.group = s.Decimal, s.Group
	if s.Group == ' ' {
		l.group = nbsp
	}
	l.spaceGroup = s.Group == ' '
	l.apostropheGroup = s.Group == '\''
	l.minGrouping = 1
	return l
}

var allOrders = []string{"mdy", "dmy", "ymd"}
