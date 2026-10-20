package projectevents

import (
	"reflect"
	"testing"
)

func TestDiff(t *testing.T) {
	t.Run("nil when equal", func(t *testing.T) {
		old := map[string]any{"name": "A", "threshold": 75}
		new := map[string]any{"name": "A", "threshold": 75}
		if got := Diff(old, new); got != nil {
			t.Fatalf("expected nil for equal maps, got %v", got)
		}
	})

	t.Run("nil for two empty maps", func(t *testing.T) {
		if got := Diff(map[string]any{}, map[string]any{}); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("only changed keys, from/to shape", func(t *testing.T) {
		old := map[string]any{"name": "A", "status": "draft", "client": "x"}
		new := map[string]any{"name": "B", "status": "draft", "client": "x"}
		got := Diff(old, new)
		want := map[string]Change{"name": {From: "A", To: "B"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("key removed counts as changed to nil", func(t *testing.T) {
		got := Diff(map[string]any{"deadline": "2026-09-01"}, map[string]any{})
		want := map[string]Change{"deadline": {From: "2026-09-01", To: nil}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("key added counts as changed from nil", func(t *testing.T) {
		got := Diff(map[string]any{}, map[string]any{"deadline": "2026-09-01"})
		want := map[string]Change{"deadline": {From: nil, To: "2026-09-01"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("nested values compared deeply", func(t *testing.T) {
		old := map[string]any{"langs": []string{"de", "fr"}}
		newSame := map[string]any{"langs": []string{"de", "fr"}}
		if got := Diff(old, newSame); got != nil {
			t.Fatalf("expected nil for deep-equal slices, got %v", got)
		}
		newDiff := map[string]any{"langs": []string{"de"}}
		if got := Diff(old, newDiff); got == nil {
			t.Fatal("expected change for differing slices, got nil")
		}
	})
}
