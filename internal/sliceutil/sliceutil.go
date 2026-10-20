// Package sliceutil holds small generic slice helpers.
package sliceutil

// NonNil returns s, or an empty slice when s is nil, so JSON encodes it as []
// and a query gets an empty array rather than NULL.
func NonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
