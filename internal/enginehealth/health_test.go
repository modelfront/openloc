package enginehealth

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// The verdict every surface reads — engine lists, router warnings, and the
// pipeline's decision to fail a job.
func TestOf(t *testing.T) {
	cases := []struct {
		name                      string
		enabled, needsKey, hasKey bool
		want                      Reason
	}{
		{"usable", true, true, true, OK},
		{"switched off", false, true, true, Disabled},
		{"key deleted", true, true, false, NoKey},
		// A deployment engine needs no key, so a null token is correct there
		// rather than broken — LibreTranslate must not read as unusable.
		{"needs no key", true, false, false, OK},
		// Off wins: it is the state the user chose, and the one they undo first.
		{"off and keyless", false, true, false, Disabled},
	}
	for _, c := range cases {
		if got := Of(c.enabled, c.needsKey, c.hasKey); got != c.want {
			t.Errorf("%s: Of(%v,%v,%v) = %q, want %q", c.name, c.enabled, c.needsKey, c.hasKey, got, c.want)
		}
	}
}

// A router leads with the breakage, not the choice: switching an engine back on
// is a click, repointing it is not.
func TestWorst(t *testing.T) {
	if got := Worst(OK, Disabled, NoKey); got != NoKey {
		t.Errorf("Worst with a no_key = %q, want no_key", got)
	}
	if got := Worst(OK, Disabled, OK); got != Disabled {
		t.Errorf("Worst with a disabled = %q, want disabled", got)
	}
	if got := Worst(OK, OK); got != OK {
		t.Errorf("Worst of usable engines = %q, want OK", got)
	}
	// A router with no engines at all is not broken — it is unconfigured, which
	// is a different thing and has its own empty state.
	if got := Worst(); got != OK {
		t.Errorf("Worst of nothing = %q, want OK", got)
	}
}

func TestNewLookup(t *testing.T) {
	l := NewLookup(
		[]db.MtEngine{
			{ID: 1, EngineType: "deepl", Enabled: true, TokenID: ptr.To[int64](9)},
			{ID: 2, EngineType: "deepl", Enabled: true},          // key deleted
			{ID: 3, EngineType: "libretranslate", Enabled: true}, // needs none
			{ID: 4, EngineType: "deepl", Enabled: false, TokenID: ptr.To[int64](9)},
		},
		[]db.AiEngine{{ID: 10, Enabled: true}, {ID: 11, Enabled: true}, {ID: 12, Enabled: false}},
		[]db.AiEngineRow{
			{EngineID: 10, TokenID: ptr.To[int64](9)},
			{EngineID: 11, TokenID: nil}, // key deleted
			{EngineID: 12, TokenID: ptr.To[int64](9)},
		},
		func(t string) bool { return t == "deepl" },
	)

	for id, want := range map[int64]Reason{1: OK, 2: NoKey, 3: OK, 4: Disabled} {
		if got := l.MT(id); got != want {
			t.Errorf("MT(%d) = %q, want %q", id, got, want)
		}
	}
	for id, want := range map[int64]Reason{10: OK, 11: NoKey, 12: Disabled} {
		if got := l.AI(id); got != want {
			t.Errorf("AI(%d) = %q, want %q", id, got, want)
		}
	}
	// An engine this tenant does not own answers OK: a router cannot reference
	// it, so flagging it would be a warning nobody could act on.
	if got := l.MT(999); got != OK {
		t.Errorf("MT(unknown) = %q, want OK", got)
	}
	// An AI engine with NO rows cannot authenticate at all.
	empty := NewLookup(nil, []db.AiEngine{{ID: 20, Enabled: true}}, nil, nil)
	if got := empty.AI(20); got != NoKey {
		t.Errorf("AI engine with no rows = %q, want no_key", got)
	}
}
