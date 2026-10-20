package testutil

import "fmt"

// Show renders p for a failure message: its value, or <nil>, so a message
// tells a missing value from an empty one.
func Show[T any](p *T) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprint(*p)
}
