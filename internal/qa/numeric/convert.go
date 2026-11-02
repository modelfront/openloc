package numeric

import (
	"math"
	"strconv"
	"strings"
)

func formatFloat(v float64, decimals int) string {
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// reading is one value a number can stand for, and the place of its last
// written digit: -2 for "8.05", 0 for "8", 1 for "60" (a trailing zero may
// be rounding).
type reading struct {
	v     float64
	place int
}

// readingsOf parses the values of a number (see numberValues).
func readingsOf(values []string) []reading {
	var out []reading
	for _, s := range values {
		if strings.HasPrefix(s, "=") {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		place := 0
		if i := strings.IndexByte(s, '.'); i >= 0 {
			place = -(len(s) - i - 1)
		} else {
			digits := strings.TrimLeft(s, "-")
			for place < len(digits)-1 && digits[len(digits)-1-place] == '0' {
				place++
			}
		}
		out = append(out, reading{v: v, place: place})
	}
	return out
}

// convert expresses v in from as a value in to.
func convert(v float64, from, to *unit) float64 {
	return (v*from.factor + from.offset - to.offset) / to.factor
}

// approx reports a target value t that is c rounded the way the target writes
// it: within half a unit of its last written digit (at most 10% off), or
// within 2% either way. Temperatures may also be a degree off.
func approx(c float64, t reading, temperature bool) bool {
	tol := math.Max(0.02*math.Abs(c), math.Min(0.5*math.Pow(10, float64(t.place)), 0.1*math.Abs(c)))
	if temperature {
		tol = math.Max(tol, 1)
	}
	return math.Abs(t.v-c) <= tol+1e-9
}

// converts reports a target number in unit to that is the source quantity
// converted. A temperature difference ("+10 °F" → "+5.6 °C") converts
// without the offset.
func converts(src quantity, tgt number, to *unit) bool {
	temperature := to.dim == "temperature"
	for _, s := range src.num.readings {
		for _, t := range tgt.readings {
			if approx(convert(s.v, src.unit, to), t, temperature) {
				return true
			}
			if temperature && approx(s.v*src.unit.factor/to.factor, t, true) {
				return true
			}
		}
	}
	return false
}

// expected writes the source quantity converted to unit to, rounded like the
// target number.
func expected(src quantity, tgt number, to *unit) string {
	rs, rt := src.num.readings, tgt.readings
	if len(rs) == 0 {
		return ""
	}
	c := convert(rs[0].v, src.unit, to)
	decimals := 0
	if len(rt) > 0 && rt[0].place < 0 {
		decimals = -rt[0].place
	} else if math.Abs(c) < 10 {
		decimals = 1
	}
	return formatFloat(c, decimals) + " " + to.symbol
}
