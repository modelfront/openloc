package numeric

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// unit is a unit of measurement: its dimension and how to convert it to the
// dimension's base unit (base = value*factor + offset).
type unit struct {
	symbol    string // how messages write it
	dim       string
	factor    float64
	offset    float64
	metric    bool
	symbols   []string // matched as written
	words     []string // matched in any letter case
	commonOut bool     // a plain target number may be in this unit
}

// units are the units the Units check knows. The base units are the metre,
// kilogram, litre, degree Celsius, square metre and metre per second.
var units = []unit{
	{symbol: "mm", dim: "length", factor: 0.001, metric: true, symbols: []string{"mm"}, words: []string{"millimeter", "millimeters", "millimetre", "millimetres"}, commonOut: true},
	{symbol: "cm", dim: "length", factor: 0.01, metric: true, symbols: []string{"cm"}, words: []string{"centimeter", "centimeters", "centimetre", "centimetres"}, commonOut: true},
	{symbol: "m", dim: "length", factor: 1, metric: true, symbols: []string{"m"}, words: []string{"meter", "meters", "metre", "metres"}, commonOut: true},
	{symbol: "km", dim: "length", factor: 1000, metric: true, symbols: []string{"km"}, words: []string{"kilometer", "kilometers", "kilometre", "kilometres"}, commonOut: true},
	{symbol: "in", dim: "length", factor: 0.0254, symbols: []string{"in."}, words: []string{"inch", "inches"}, commonOut: true},
	{symbol: "ft", dim: "length", factor: 0.3048, symbols: []string{"ft", "ft."}, words: []string{"foot", "feet"}, commonOut: true},
	{symbol: "yd", dim: "length", factor: 0.9144, symbols: []string{"yd", "yds"}, words: []string{"yard", "yards"}, commonOut: true},
	{symbol: "mi", dim: "length", factor: 1609.344, symbols: []string{"mi"}, words: []string{"mile", "miles"}, commonOut: true},

	{symbol: "mg", dim: "mass", factor: 1e-6, metric: true, symbols: []string{"mg"}, words: []string{"milligram", "milligrams"}},
	{symbol: "g", dim: "mass", factor: 0.001, metric: true, symbols: []string{"g"}, words: []string{"gram", "grams", "gramme", "grammes"}, commonOut: true},
	{symbol: "kg", dim: "mass", factor: 1, metric: true, symbols: []string{"kg"}, words: []string{"kilogram", "kilograms", "kilogramme", "kilogrammes"}, commonOut: true},
	{symbol: "oz", dim: "mass", factor: 0.028349523125, symbols: []string{"oz"}, words: []string{"ounce", "ounces"}, commonOut: true},
	{symbol: "lb", dim: "mass", factor: 0.45359237, symbols: []string{"lb", "lbs"}, words: []string{"pound", "pounds"}, commonOut: true},

	{symbol: "ml", dim: "volume", factor: 0.001, metric: true, symbols: []string{"ml", "mL"}, words: []string{"milliliter", "milliliters", "millilitre", "millilitres"}, commonOut: true},
	{symbol: "cl", dim: "volume", factor: 0.01, metric: true, symbols: []string{"cl", "cL"}},
	{symbol: "dl", dim: "volume", factor: 0.1, metric: true, symbols: []string{"dl", "dL"}},
	{symbol: "l", dim: "volume", factor: 1, metric: true, symbols: []string{"l", "L"}, words: []string{"liter", "liters", "litre", "litres"}, commonOut: true},
	{symbol: "fl oz", dim: "volume", factor: 0.0295735295625, symbols: []string{"fl oz", "fl. oz.", "fl. oz"}, words: []string{"fluid ounce", "fluid ounces"}},
	{symbol: "pt", dim: "volume", factor: 0.473176473, words: []string{"pint", "pints"}},
	{symbol: "qt", dim: "volume", factor: 0.946352946, symbols: []string{"qt"}, words: []string{"quart", "quarts"}},
	{symbol: "gal", dim: "volume", factor: 3.785411784, symbols: []string{"gal"}, words: []string{"gallon", "gallons"}, commonOut: true},

	{symbol: "°C", dim: "temperature", factor: 1, metric: true, symbols: []string{"°C", "° C", "ºC", "℃"}, words: []string{"degrees Celsius", "degrees centigrade"}, commonOut: true},
	{symbol: "°F", dim: "temperature", factor: 5.0 / 9, offset: -32 * 5.0 / 9, symbols: []string{"°F", "° F", "ºF", "℉"}, words: []string{"degrees Fahrenheit"}, commonOut: true},

	{symbol: "cm²", dim: "area", factor: 1e-4, metric: true, symbols: []string{"cm²", "cm2"}},
	{symbol: "m²", dim: "area", factor: 1, metric: true, symbols: []string{"m²", "m2"}, words: []string{"square meter", "square meters", "square metre", "square metres"}, commonOut: true},
	{symbol: "km²", dim: "area", factor: 1e6, metric: true, symbols: []string{"km²", "km2"}, words: []string{"square kilometer", "square kilometers", "square kilometre", "square kilometres"}, commonOut: true},
	{symbol: "ha", dim: "area", factor: 1e4, metric: true, symbols: []string{"ha"}, words: []string{"hectare", "hectares"}, commonOut: true},
	{symbol: "sq in", dim: "area", factor: 0.00064516, symbols: []string{"in²", "sq in", "sq. in."}, words: []string{"square inch", "square inches"}},
	{symbol: "sq ft", dim: "area", factor: 0.09290304, symbols: []string{"ft²", "sq ft", "sq. ft.", "sq. ft"}, words: []string{"square foot", "square feet"}, commonOut: true},
	{symbol: "sq mi", dim: "area", factor: 2589988.110336, symbols: []string{"mi²", "sq mi", "sq. mi."}, words: []string{"square mile", "square miles"}, commonOut: true},
	{symbol: "acre", dim: "area", factor: 4046.8564224, words: []string{"acre", "acres"}, commonOut: true},

	{symbol: "km/h", dim: "speed", factor: 1 / 3.6, metric: true, symbols: []string{"km/h", "km/hr", "kmh", "kph"}, commonOut: true},
	{symbol: "m/s", dim: "speed", factor: 1, metric: true, symbols: []string{"m/s"}},
	{symbol: "mph", dim: "speed", factor: 0.44704, symbols: []string{"mph", "mi/h"}, commonOut: true},
	{symbol: "ft/s", dim: "speed", factor: 0.3048, symbols: []string{"ft/s"}},
}

// unitName is one name a unit is written with, longest first so "km/h" wins
// over "km".
type unitName struct {
	name string
	fold bool // match in any letter case
	unit *unit
}

var unitNames = func() []unitName {
	var out []unitName
	for i := range units {
		u := &units[i]
		for _, s := range u.symbols {
			out = append(out, unitName{name: s, unit: u})
		}
		for _, w := range u.words {
			out = append(out, unitName{name: w, fold: true, unit: u})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].name) > len(out[j].name) })
	return out
}()

func unitBySymbol(symbol string) *unit {
	for i := range units {
		if units[i].symbol == symbol {
			return &units[i]
		}
	}
	panic("numeric: no unit " + symbol)
}

// feetInches matches 5'10" — the only way an apostrophe is a unit.
var feetInches = regexp.MustCompile(`(\d{1,2})'\s?(\d{1,2})"`)

// unitAt returns the unit written at norm byte offset i (after an optional
// space) and where it ends.
func (t *text) unitAt(i int) (*unit, int) {
	s := t.norm
	j := i
	if j < len(s) && s[j] == ' ' {
		j++
	} else if r, size := utf8.DecodeRuneInString(s[j:]); r == nbsp {
		j += size
	}
	for _, n := range unitNames {
		end := j + len(n.name)
		if end > len(s) {
			continue
		}
		got := s[j:end]
		if got != n.name && !(n.fold && strings.EqualFold(got, n.name)) {
			continue
		}
		if end < len(s) {
			if r, _ := utf8.DecodeRuneInString(s[end:]); isWordRune(r) {
				continue
			}
		}
		return n.unit, end
	}
	return nil, i
}

// quantityCandidates finds the numbers written with a unit.
func (t *text) quantityCandidates(loc locale) []candidate {
	var out []candidate
	for _, n := range t.scanNumbers(0, len(t.norm), loc) {
		end := n.end
		for end > n.start && (t.norm[end-1] == '.' || t.norm[end-1] == ',') {
			end-- // "5 km." ends the sentence: the number is "5"
		}
		u, uend := t.unitAt(end)
		if u == nil {
			continue
		}
		// "$5m" is five million.
		if prev, _ := utf8.DecodeLastRuneInString(t.norm[:n.start]); u.symbol == "m" && strings.ContainsRune("$€£¥", prev) {
			continue
		}
		num := t.newNumber(n.start, end, loc)
		out = append(out, candidate{kind: kindQuantity, start: n.start, end: uend, qty: &quantity{num: num, unit: u}})
	}
	for _, m := range feetInches.FindAllStringSubmatchIndex(t.norm, -1) {
		if !t.boundaryOK(m[0], m[1]) {
			continue
		}
		ft, in := atoi(t.norm[m[2]:m[3]]), atoi(t.norm[m[4]:m[5]])
		inches := unitBySymbol("in")
		num := number{start: m[0], end: m[1], text: t.original(m[0], m[1]), norm: t.norm[m[0]:m[1]]}
		num.values = []string{formatFloat(float64(ft*12+in), -1)}
		num.readings = readingsOf(num.values)
		out = append(out, candidate{kind: kindQuantity, start: m[0], end: m[1], qty: &quantity{num: num, unit: inches}})
	}
	return out
}

// quantity is a number written with a unit.
type quantity struct {
	num  number
	unit *unit
}
