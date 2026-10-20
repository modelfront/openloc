package spellcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

// mkMatch builds a match in the joined text's coordinate space (UTF-16 units).
func mkMatch(offset, length int) Match {
	m := Match{Offset: offset, Length: length}
	m.Rule.Category.ID = "TYPOS"
	return m
}

func TestSplitJoinedMatches(t *testing.T) {
	// UTF-16 lengths: "Prüfung öffnen." = 15, "die adresse" = 11, "ok 😀 ok" = 8
	// (the emoji is a surrogate pair). Separator "\n\n" = 2 units.
	texts := []string{"Prüfung öffnen.", "die adresse", "ok 😀 ok"}
	// Joined layout: seg0 [0,15), sep [15,17), seg1 [17,28), sep [28,30), seg2 [30,38).

	matches := []Match{
		mkMatch(0, 7),  // "Prüfung" in seg0 → local 0
		mkMatch(21, 7), // "adresse" in seg1 → local 4
		mkMatch(33, 2), // the emoji in seg2 → local 3
		mkMatch(13, 5), // crosses seg0's end into the separator → dropped
		mkMatch(16, 3), // starts in the separator, ends in seg1 → dropped
	}

	got := splitJoinedMatches(texts, matches)

	if len(got[0]) != 1 || got[0][0].Offset != 0 || got[0][0].Length != 7 {
		t.Errorf("seg0 matches = %+v, want one at local offset 0 len 7", got[0])
	}
	if len(got[1]) != 1 || got[1][0].Offset != 4 {
		t.Errorf("seg1 matches = %+v, want one at local offset 4", got[1])
	}
	if len(got[2]) != 1 || got[2][0].Offset != 3 {
		t.Errorf("seg2 matches = %+v, want one at local offset 3", got[2])
	}

	// The seg1 match's local offset must slice out the flagged word — this is
	// what the dictionary filter and the UI both do with it.
	units := utf16.Encode([]rune(texts[1]))
	word := string(utf16.Decode(units[got[1][0].Offset : got[1][0].Offset+got[1][0].Length]))
	if word != "adresse" {
		t.Errorf("seg1 local offset slices %q, want \"adresse\"", word)
	}
}

func TestGroupBatch(t *testing.T) {
	mk := func(n int, text string) []BatchItem {
		items := make([]BatchItem, n)
		for i := range items {
			items[i] = BatchItem{ID: int64(i), Text: text}
		}
		return items
	}
	all := func(n int) []int {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	groupLens := func(groups [][]int) []int {
		lens := make([]int, len(groups))
		for i, g := range groups {
			lens[i] = len(g)
		}
		return lens
	}

	t.Run("small batch prefers full groups over parallelism", func(t *testing.T) {
		items := mk(12, "kurzer Text")
		groups := groupBatch(items, all(12))
		// ceil(12/8) = 2 is below the floor → targetSize 10 → groups of 10 + 2.
		if len(groups) != 2 {
			t.Fatalf("got %d groups, want 2 (minGroupSegments floor)", len(groups))
		}
		if len(groups[0]) != minGroupSegments || len(groups[1]) != 2 {
			t.Errorf("group sizes = %d, %d; want %d, 2", len(groups[0]), len(groups[1]), minGroupSegments)
		}
	})

	t.Run("a client chunk of 20 becomes two full requests", func(t *testing.T) {
		groups := groupBatch(mk(20, "kurzer Text"), all(20))
		if len(groups) != 2 || len(groups[0]) != 10 || len(groups[1]) != 10 {
			t.Fatalf("got %d groups %v, want 2 groups of 10", len(groups), groupLens(groups))
		}
	})

	t.Run("large batch respects the segment cap", func(t *testing.T) {
		items := mk(400, "kurzer Text")
		for _, g := range groupBatch(items, all(400)) {
			if len(g) > maxGroupSegments {
				t.Fatalf("group of %d exceeds maxGroupSegments %d", len(g), maxGroupSegments)
			}
		}
	})

	t.Run("unit budget splits oversized groups", func(t *testing.T) {
		// Two of these texts together blow the unit budget, so the budget —
		// not the segment-count target — must split every pair into singles.
		long := strings.Repeat("x", maxGroupUnits/2+1)
		items := mk(16, long)
		groups := groupBatch(items, all(16))
		if len(groups) != 16 {
			t.Fatalf("got %d groups, want 16 (unit budget must forbid pairing these texts)", len(groups))
		}
	})

	t.Run("no missed items yields no groups", func(t *testing.T) {
		if got := groupBatch(mk(3, "text"), nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// ltCountingStub fakes /v2/check like the integration stub, additionally
// counting requests and optionally failing when the text contains `failOn`.
func ltCountingStub(t *testing.T, flag, failOn string, requests *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		text := r.Form.Get("text")
		if failOn != "" && strings.Contains(text, failOn) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		matches := []map[string]any{}
		// Report every occurrence of `flag`, offsets in UTF-16 units.
		units := utf16.Encode([]rune(text))
		flagUnits := utf16.Encode([]rune(flag))
		for i := 0; i+len(flagUnits) <= len(units); i++ {
			if string(utf16.Decode(units[i:i+len(flagUnits)])) == flag {
				matches = append(matches, map[string]any{
					"message":      "typo",
					"offset":       i,
					"length":       len(flagUnits),
					"replacements": []map[string]string{{"value": "Fixed"}},
					"rule": map[string]any{
						"id":       "SPELLER",
						"category": map[string]string{"id": "TYPOS", "name": "Typos"},
					},
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	}))
}

func TestCheckBatchJoinsRequests(t *testing.T) {
	var requests atomic.Int64
	lt := ltCountingStub(t, "tippfehler", "", &requests)
	defer lt.Close()
	client := NewClient(lt.URL)

	items := make([]BatchItem, 20)
	for i := range items {
		// Umlauts ahead of the flagged word keep the UTF-16 math honest, and
		// distinct texts defeat the cache.
		items[i] = BatchItem{ID: int64(i + 1), Text: fmt.Sprintf("Prüfung %d: ein tippfehler hier.", i)}
	}
	// Segments 5 and 12 are clean.
	items[5].Text = "Prüfung fünf ist sauber."
	items[12].Text = "Prüfung zwölf ist sauber."

	results := client.CheckBatch(context.Background(), items, "de")

	// 20 short segments → two full joined requests (minGroupSegments floor).
	if got := requests.Load(); got > 2 {
		t.Errorf("made %d LT requests for 20 segments, want 2 — joining is not happening", got)
	}
	for i, res := range results {
		if res.Err != nil {
			t.Fatalf("item %d: unexpected error: %v", i, res.Err)
		}
		if res.ID != items[i].ID {
			t.Errorf("item %d: ID = %d, want %d (order must be preserved)", i, res.ID, items[i].ID)
		}
		if i == 5 || i == 12 {
			if len(res.Matches) != 0 {
				t.Errorf("clean item %d got matches: %+v", i, res.Matches)
			}
			continue
		}
		if len(res.Matches) != 1 {
			t.Fatalf("item %d: got %d matches, want 1", i, len(res.Matches))
		}
		m := res.Matches[0]
		units := utf16.Encode([]rune(items[i].Text))
		word := string(utf16.Decode(units[m.Offset : m.Offset+m.Length]))
		if word != "tippfehler" {
			t.Errorf("item %d: local offset slices %q, want \"tippfehler\"", i, word)
		}
	}

	// Second run over the same items must be answered from the cache.
	before := requests.Load()
	client.CheckBatch(context.Background(), items, "de")
	if got := requests.Load(); got != before {
		t.Errorf("re-run made %d extra LT requests, want 0 (cache)", got-before)
	}
}

func TestCheckBatchIsolatesGroupFailures(t *testing.T) {
	var requests atomic.Int64
	lt := ltCountingStub(t, "tippfehler", "kaboom", &requests)
	defer lt.Close()
	client := NewClient(lt.URL)

	items := make([]BatchItem, 50)
	for i := range items {
		items[i] = BatchItem{ID: int64(i + 1), Text: fmt.Sprintf("Segment %d mit tippfehler.", i)}
	}
	items[3].Text = "Dieses Segment sagt kaboom."

	results := client.CheckBatch(context.Background(), items, "de")

	var failed, succeeded int
	for _, res := range results {
		if res.Err != nil {
			failed++
		} else {
			succeeded++
		}
	}
	if failed == 0 {
		t.Fatal("the poisoned group reported no errors")
	}
	// 50 items → groups of 10, so only the poisoned segment's group may fail;
	// everything else must succeed.
	if failed > minGroupSegments {
		t.Errorf("%d segments failed, want at most the poisoned group of %d", failed, minGroupSegments)
	}
	if succeeded < 40 {
		t.Errorf("only %d segments succeeded — a single bad group must not sink the batch", succeeded)
	}
}

func TestCheckBatchSkipsEmptySegments(t *testing.T) {
	var requests atomic.Int64
	lt := ltCountingStub(t, "tippfehler", "", &requests)
	defer lt.Close()
	client := NewClient(lt.URL)

	items := []BatchItem{
		{ID: 1, Text: "ein tippfehler"},
		{ID: 2, Text: "   "},
		{ID: 3, Text: ""},
	}
	results := client.CheckBatch(context.Background(), items, "de")

	if results[0].Err != nil || len(results[0].Matches) != 1 {
		t.Errorf("item 0: %+v, want one match", results[0])
	}
	for _, i := range []int{1, 2} {
		if results[i].Err != nil || results[i].Matches != nil {
			t.Errorf("blank item %d must be silently clean, got %+v", i, results[i])
		}
	}
}

func TestDoCheckDisablesSuppressedCategories(t *testing.T) {
	var gotParam string
	lt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotParam = r.Form.Get("disabledCategories")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"matches":[]}`))
	}))
	defer lt.Close()

	client := NewClient(lt.URL)
	if _, err := client.Check(context.Background(), "text", "de"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if gotParam != "TYPOGRAPHY,WHITESPACE" {
		t.Errorf("disabledCategories = %q, want TYPOGRAPHY,WHITESPACE", gotParam)
	}
}
