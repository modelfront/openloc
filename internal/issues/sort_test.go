package issues

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/ptr"
)

func TestParseSort(t *testing.T) {
	ok := map[string]struct {
		raw      string
		platform bool
		want     []SortLevel
	}{
		"empty is no level":       {"", false, nil},
		"updated is oldest first": {"updated", false, []SortLevel{{"updated", false}}},
		"three levels":            {"status,-kind,updated", false, []SortLevel{{"status", false}, {"kind", true}, {"updated", false}}},
		"platform keys":           {"-priority,assignee", true, []SortLevel{{"priority", true}, {"assignee", false}}},
	}
	for name, c := range ok {
		got, err := ParseSort(c.raw, c.platform)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseSort(%q) = %v, %v; want %v", name, c.raw, got, err, c.want)
		}
	}
	bad := map[string]struct {
		raw      string
		platform bool
	}{
		"four levels":           {"status,kind,area,updated", true},
		"unknown key":           {"title", true},
		"a key twice":           {"status,-status", false},
		"an empty key":          {"status,,updated", false},
		"a lone minus":          {"-", false},
		"priority for a member": {"priority", false},
		"assignee for a member": {"assignee", false},
		"SQL is not a key":      {"updated;DROP TABLE issues", true},
	}
	for name, c := range bad {
		if got, err := ParseSort(c.raw, c.platform); err == nil {
			t.Errorf("%s: ParseSort(%q) = %v, want an error", name, c.raw, got)
		}
	}
}

// Every order ends with the newest report and the id, so each row has one place.
func TestFullOrderTieBreakers(t *testing.T) {
	columns := func(order []orderedPart) []string {
		out := make([]string, len(order))
		for i, p := range order {
			out[i] = p.column + map[bool]string{true: " desc", false: " asc"}[p.desc]
		}
		return out
	}
	cases := map[string]struct {
		levels   []SortLevel
		platform bool
		want     []string
	}{
		"the default":            {nil, false, []string{"i.created_at desc", "i.id desc"}},
		"oldest update first":    {[]SortLevel{{"updated", false}}, false, []string{"i.updated_at asc", "i.created_at desc", "i.id desc"}},
		"status then the newest": {[]SortLevel{{"status", false}}, false, []string{"i.status asc", "i.created_at desc", "i.id desc"}},
		"kind is two parts": {[]SortLevel{{"kind", true}, {"updated", false}}, false,
			[]string{"i.kind desc", "i.severity desc", "i.updated_at asc", "i.created_at desc", "i.id desc"}},
		"reporter per scope": {[]SortLevel{{"reporter", false}}, true,
			[]string{"t.name asc", "u.email asc", "i.created_at desc", "i.id desc"}},
	}
	for name, c := range cases {
		if got := columns(fullOrder(c.levels, c.platform)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// Every value an issue can have has one place in its order; a missing one
// would sort after all the others.
func TestSortOrdersAreComplete(t *testing.T) {
	for name, c := range map[string]struct {
		order  []string
		labels map[string]string
	}{
		"status":   {statusOrder, statusLabels},
		"priority": {priorityOrder, priorityLabels},
		"kind":     {kindOrder, kindLabels},
		"severity": {severityOrder, severityLabels},
		"area":     {areaOrder, areaLabels},
	} {
		got := append([]string(nil), c.order...)
		want := make([]string, 0, len(c.labels))
		for k := range c.labels {
			want = append(want, k)
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s order %v does not list every value once: %v", name, c.order, want)
		}
	}
	for i := 1; i < len(areaOrder); i++ {
		if areaLabels[areaOrder[i-1]] > areaLabels[areaOrder[i]] {
			t.Errorf("areas not by label: %q before %q", areaLabels[areaOrder[i-1]], areaLabels[areaOrder[i]])
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	levels := []SortLevel{{"assignee", false}, {"priority", true}}
	order, sig := fullOrder(levels, true), sortSignature(levels, true)
	at := time.Date(2026, 10, 21, 9, 30, 0, 123456000, time.UTC)
	row := issueRow{ID: 42, AssigneeName: ptr.To("Zoë"), CreatedAt: pgtype.Timestamptz{Time: at, Valid: true}}
	raw, err := encodeCursor(sig, order, row)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCursor(raw, sig, order)
	if err != nil {
		t.Fatal(err)
	}
	// assignee (twice: unassigned first, then the name), priority, created, id.
	want := []any{"Zoë", "Zoë", nil, at.Local(), int64(42)}
	if len(got) != len(want) {
		t.Fatalf("values %v, want %v", got, want)
	}
	for i := range want {
		if g, ok := got[i].(time.Time); ok {
			if !g.Equal(at) {
				t.Errorf("value %d = %v, want %v", i, g, at)
			}
			continue
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("value %d = %#v, want %#v", i, got[i], want[i])
		}
	}
	if first, err := decodeCursor("", sig, order); err != nil || first != nil {
		t.Errorf("empty cursor = %v, %v; want the first page", first, err)
	}
	other := sortSignature([]SortLevel{{"assignee", true}, {"priority", true}}, true)
	for name, c := range map[string]string{"another order": other, "the members' list": sortSignature(levels, false)} {
		if _, err := decodeCursor(raw, c, fullOrder(levels, true)); err == nil {
			t.Errorf("a cursor for %s is accepted", name)
		}
	}
	for _, bad := range []string{"not base64!", "e30", "eyJzIjoibToiLCJ2IjpbXX0"} {
		if _, err := decodeCursor(bad, sig, order); err == nil {
			t.Errorf("cursor %q is accepted", bad)
		}
	}
	// The right order and length, with a value of the wrong type in one place:
	// assignee (text, text), priority (text), created (timestamptz), id (bigint).
	good := []any{"Zoë", "Zoë", nil, int64(1), int64(42)}
	for name, c := range map[string]struct {
		at int
		v  any
	}{
		"a number for a name":   {0, 7},
		"text for a time":       {3, "2026-10-21"},
		"a fraction for a time": {3, 1.5},
		"text for an id":        {4, "42"},
		"a fraction for an id":  {4, 4.2},
		"null for an id":        {4, nil},
	} {
		values := append([]any(nil), good...)
		values[c.at] = c.v
		body, err := json.Marshal(cursorBody{Sort: sig, Values: values})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeCursor(base64.RawURLEncoding.EncodeToString(body), sig, order); err == nil {
			t.Errorf("a cursor with %s is accepted", name)
		}
	}
	body, err := json.Marshal(cursorBody{Sort: sig, Values: good})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCursor(base64.RawURLEncoding.EncodeToString(body), sig, order); err != nil {
		t.Errorf("the well-typed cursor is refused: %v", err)
	}
}

// One direction compares the columns as a row, which an index can serve;
// mixed directions spell the comparison out.
func TestListSQLKeyset(t *testing.T) {
	newest := listSQL("SELECT", fullOrder(nil, false), true)
	if !strings.Contains(newest, "AND ((i.created_at, i.id) < (@c0::timestamptz, @c1::bigint))") ||
		!strings.Contains(newest, "ORDER BY i.created_at DESC, i.id DESC") {
		t.Errorf("the default:\n%s", newest)
	}
	updated := listSQL("SELECT", fullOrder([]SortLevel{{"updated", true}}, false), true)
	if !strings.Contains(updated, "((i.updated_at, i.created_at, i.id) < (") {
		t.Errorf("newest update first:\n%s", updated)
	}
	mixed := listSQL("SELECT", fullOrder([]SortLevel{{"status", false}}, false), true)
	for _, want := range []string{" OR ", "i.created_at < @c1::timestamptz", "i.status), 7)", "ORDER BY COALESCE("} {
		if !strings.Contains(mixed, want) {
			t.Errorf("mixed directions lack %q:\n%s", want, mixed)
		}
	}
	if first := listSQL("SELECT", fullOrder(nil, false), false); strings.Contains(first, "@c0") {
		t.Errorf("the first page has a cursor condition:\n%s", first)
	}
}
