package translationmemory

import "testing"

// 0 is a real threshold ("show every candidate"); only a negative value falls
// back to the default.
func TestFuzzyThreshold(t *testing.T) {
	for _, c := range []struct{ in, want float32 }{
		{-1, DefaultFuzzyThreshold},
		{-0.01, DefaultFuzzyThreshold},
		{0, 0},
		{0.6, 0.6},
		{1, 1},
	} {
		if got := FuzzyThreshold(c.in); got != c.want {
			t.Errorf("FuzzyThreshold(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
