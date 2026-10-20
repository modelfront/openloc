package metrics

import "testing"

// Every value column declares a unit. The chart builder decides whether two
// columns can share an axis by comparing them, so a blank one silently means
// "combines with every other column", which is the opposite of safe.
func TestEveryValueColumnHasAUnit(t *testing.T) {
	for name, m := range measures {
		for _, v := range m.values {
			if v.unit == "" {
				t.Errorf("%s.%s has no unit", name, v.name)
			}
		}
	}
}

// Grains are dimensions to the compiler but not to a picker: they are buckets
// of time, not things to split by.
func TestCategoricalDimensionsExcludeGrains(t *testing.T) {
	for name := range measures {
		for _, d := range CategoricalDimensionsFor(name) {
			if IsTimeGrain(d) {
				t.Errorf("%s lists the time grain %q as a categorical dimension", name, d)
			}
		}
	}
}
