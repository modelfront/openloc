// Package ptr holds the helpers for optional values that many packages need:
// sqlc maps a nullable column to a pointer.
package ptr

// To points at a copy of v: a literal for an optional field.
func To[T any](v T) *T { return &v }

// Deref is what p points to, or T's zero value when p is nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// NilIfZero is nil for T's zero value ("" or 0), else a pointer to v: an
// empty text or a missing id stored as NULL.
func NilIfZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// Equal reports whether two optional values hold the same thing: both unset,
// or both set to an equal value.
func Equal[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
