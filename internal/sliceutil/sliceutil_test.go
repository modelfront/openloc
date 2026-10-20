package sliceutil

import (
	"encoding/json"
	"testing"
)

func TestNonNil(t *testing.T) {
	if got := NonNil[string](nil); got == nil || len(got) != 0 {
		t.Errorf("NonNil(nil) = %#v, want an empty slice", got)
	}
	if b, _ := json.Marshal(NonNil[int64](nil)); string(b) != "[]" {
		t.Errorf("NonNil(nil) encodes as %s, want []", b)
	}
	in := []string{"a"}
	if got := NonNil(in); &got[0] != &in[0] {
		t.Errorf("NonNil copied a non-nil slice")
	}
}
