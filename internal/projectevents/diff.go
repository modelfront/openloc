package projectevents

import "reflect"

// Change is one field's old→new pair inside details["changes"].
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Diff returns the keys whose values differ between old and new, as
// {field: {from, to}}. Keys present in only one map count as changed (the
// missing side renders as null). Returns nil when nothing changed so callers
// can skip emitting no-op events:
//
//	if changes := projectevents.Diff(old, new); changes != nil {
//	    projectevents.Emit(ctx, q, projectevents.Event{..., Details: map[string]any{"changes": changes}})
//	}
func Diff(old, new map[string]any) map[string]Change {
	changes := map[string]Change{}
	for k, ov := range old {
		nv, ok := new[k]
		if !ok || !reflect.DeepEqual(ov, nv) {
			changes[k] = Change{From: ov, To: nv}
		}
	}
	for k, nv := range new {
		if _, ok := old[k]; !ok {
			changes[k] = Change{From: nil, To: nv}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}
