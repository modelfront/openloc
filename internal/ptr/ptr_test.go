package ptr_test

import (
	"testing"

	"openloc/internal/ptr"
)

func TestTo(t *testing.T) {
	p := ptr.To("x")
	if p == nil || *p != "x" {
		t.Errorf("To(x) = %v", p)
	}
	*p = "y"
	if q := ptr.To("x"); *q != "x" {
		t.Error("To shares storage between calls")
	}
}

func TestDeref(t *testing.T) {
	s := "x"
	if ptr.Deref(&s) != "x" || ptr.Deref[string](nil) != "" || ptr.Deref[int64](nil) != 0 {
		t.Error("Deref")
	}
}

func TestNilIfZero(t *testing.T) {
	if ptr.NilIfZero("") != nil || ptr.NilIfZero(int64(0)) != nil {
		t.Error("a zero value is not nil")
	}
	if p := ptr.NilIfZero("a"); p == nil || *p != "a" {
		t.Errorf("NilIfZero(a) = %v", p)
	}
	if p := ptr.NilIfZero(int64(7)); p == nil || *p != 7 {
		t.Errorf("NilIfZero(7) = %v", p)
	}
}

func TestEqual(t *testing.T) {
	one, alsoOne, two := int64(1), int64(1), int64(2)
	for _, c := range []struct {
		a, b *int64
		want bool
	}{{nil, nil, true}, {&one, nil, false}, {nil, &one, false}, {&one, &alsoOne, true}, {&one, &two, false}} {
		if got := ptr.Equal(c.a, c.b); got != c.want {
			t.Errorf("Equal(%v, %v) = %v", c.a, c.b, got)
		}
	}
}
