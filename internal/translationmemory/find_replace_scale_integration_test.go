package translationmemory_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

const (
	frMatching = 2500 // entries holding "foo" in the source
	frOther    = 500  // entries without it
)

// seedFRTM is a TM of frMatching entries "foo <i>" → "bar <i>" and frOther
// entries without the find term.
func seedFRTM(t *testing.T, pool *pgxpool.Pool, q db.Querier) (db.Tenant, db.User, db.TmCollection) {
	t.Helper()
	tenant := testutil.SeedTenant(t, q, "FRScaleCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "frscale@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "FR Scale TM")
	testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "es", frMatching+frOther, func(i int) (string, string) {
		if i <= frMatching {
			return fmt.Sprintf("foo %d", i), fmt.Sprintf("bar %d", i)
		}
		return fmt.Sprintf("other %d", i), fmt.Sprintf("bar %d", i)
	})
	return tenant, user, tm
}

type frPreview struct {
	Data struct {
		TotalEntries      int    `json:"total_entries"`
		TotalReplacements int    `json:"total_replacements"`
		NextCursor        *int64 `json:"next_cursor"`
		Matches           []struct {
			EntryID       int64  `json:"entry_id"`
			NewSourceText string `json:"new_source_text"`
		} `json:"matches"`
	} `json:"data"`
}

// The preview of a find term in thousands of entries returns one page and the
// totals, and reads only that page from the database. Following next_cursor
// reaches every match exactly once.
func TestIntegrationTMFindReplace_PreviewIsPaged(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_, user, tm := seedFRTM(t, pool, q)
	tenantID := tm.TenantID

	cpool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(cpool)
	path := fmt.Sprintf("/api/tms/%d/find-replace", tm.ID)

	log.Reset()
	rec := testutil.DoRequest(t, router, "POST", path,
		map[string]any{"find": "FOO", "replace": "baz", "mode": "find", "scope": "source"}, user.ID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var first frPreview
	testutil.DecodeJSON(t, rec, &first)

	if first.Data.TotalEntries != frMatching || first.Data.TotalReplacements != frMatching {
		t.Errorf("totals = %d entries / %d replacements, want %d / %d",
			first.Data.TotalEntries, first.Data.TotalReplacements, frMatching, frMatching)
	}
	if n := len(first.Data.Matches); n == 0 || n > 500 {
		t.Fatalf("first page has %d matches, want a bounded page", n)
	}
	if rows := log.TotalRows(); rows > 1000 {
		t.Errorf("the preview read %d rows from the database, want one page", rows)
	}
	if first.Data.NextCursor == nil {
		t.Fatal("next_cursor is null on a partial page")
	}

	seen := map[int64]bool{}
	page := first
	for pages := 1; ; pages++ {
		for _, m := range page.Data.Matches {
			if seen[m.EntryID] {
				t.Fatalf("entry %d returned twice", m.EntryID)
			}
			seen[m.EntryID] = true
			if m.NewSourceText[:4] != "baz " {
				t.Fatalf("new source %q, want the find term replaced", m.NewSourceText)
			}
		}
		if page.Data.NextCursor == nil {
			break
		}
		if pages > frMatching {
			t.Fatal("paging does not end")
		}
		rec := testutil.DoRequest(t, router, "POST", path,
			map[string]any{"find": "FOO", "replace": "baz", "mode": "find", "scope": "source", "cursor": *page.Data.NextCursor},
			user.ID, tenantID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		page = frPreview{}
		testutil.DecodeJSON(t, rec, &page)
	}
	if len(seen) != frMatching {
		t.Errorf("paging reached %d entries, want %d", len(seen), frMatching)
	}
}

// Replace all over thousands of matches stays one transaction but sends a
// bounded number of statements, and writes one history row per entry with the
// text as it was.
func TestIntegrationTMFindReplace_ReplaceAllIsSetBased(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, user, tm := seedFRTM(t, pool, q)

	cpool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(cpool)

	log.Reset()
	rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
		map[string]any{"find": "foo", "replace": "baz", "mode": "replace_all", "scope": "both"}, user.ID, tm.TenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var res struct {
		Data struct {
			EntriesUpdated    int `json:"entries_updated"`
			TotalReplacements int `json:"total_replacements"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &res)
	if res.Data.EntriesUpdated != frMatching || res.Data.TotalReplacements != frMatching {
		t.Errorf("result = %+v, want %d entries and replacements", res.Data, frMatching)
	}
	if n := log.Total(); n > 40 {
		t.Errorf("replace all sent %d statements for %d matches, want a bounded number", n, frMatching)
	}
	if n := log.CountPrefix("BEGIN"); n != 1 {
		t.Errorf("replace all opened %d transactions, want 1", n)
	}

	var replaced, untouched, history, historyBefore int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE source_text LIKE 'baz %' AND source_rich_text IS NULL),
		       count(*) FILTER (WHERE source_text LIKE 'other %')
		FROM translation_memory WHERE tm_id = $1`, tm.ID).Scan(&replaced, &untouched); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE h.source_text = 'foo ' || substr(m.source_text, 5) AND h.target_text = m.target_text
		                                   AND h.change_reason = 'find_replace' AND h.changed_by_user_id = $2)
		FROM translation_memory_history h JOIN translation_memory m ON m.id = h.entry_id
		WHERE m.tm_id = $1`, tm.ID, user.ID).Scan(&history, &historyBefore); err != nil {
		t.Fatal(err)
	}
	if replaced != frMatching || untouched != frOther {
		t.Errorf("replaced %d, untouched %d; want %d and %d", replaced, untouched, frMatching, frOther)
	}
	if history != frMatching || historyBefore != frMatching {
		t.Errorf("history rows %d (%d with the old text), want %d", history, historyBefore, frMatching)
	}
}

// The preview totals equal the sum over every page for terms holding LIKE
// wildcards, a backslash and a non-ASCII letter in another case: the SQL
// filter and counts treat them as plain text, like the replacement does.
func TestIntegrationTMFindReplace_TotalsMatchPagesForSpecialTerms(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "FRSpecialCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "frspecial@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "FR Special TM")
	for _, e := range []struct{ src, tgt string }{
		{"Save 50%_off today, 50%_off!", "Ahorra hoy"},
		{"Save 50xoff today", "Ahorra"},
		{"Ahorra", "Save 50%_off"},
		{"Open C:\\temp\\a file", "Abre C:\\temp\\a"},
		{"Open C:/temp/a file", "Abre"},
		{"ÄPFEL und äpfel", "Äpfel"},
		{"apfel", "manzana"},
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: e.src, TargetText: e.tgt, CreatedBy: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	router := testutil.NewTestRouter(pool)
	for _, tc := range []struct {
		find                  string
		entries, replacements int
	}{
		{"50%_off", 2, 3},
		{"\\temp\\a", 1, 2},
		{"äpfel", 1, 3},
	} {
		var entries, replacements int
		var first frPreview
		var cursor int64
		for page := 0; ; page++ {
			rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/tms/%d/find-replace", tm.ID),
				map[string]any{"find": tc.find, "replace": "x", "mode": "find", "scope": "both", "limit": 1, "cursor": cursor},
				user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusOK)
			var p struct {
				Data struct {
					TotalEntries      int    `json:"total_entries"`
					TotalReplacements int    `json:"total_replacements"`
					NextCursor        *int64 `json:"next_cursor"`
					Matches           []struct {
						Source int `json:"source_match_count"`
						Target int `json:"target_match_count"`
					} `json:"matches"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &p)
			if page == 0 {
				first.Data.TotalEntries, first.Data.TotalReplacements = p.Data.TotalEntries, p.Data.TotalReplacements
			}
			for _, m := range p.Data.Matches {
				entries++
				replacements += m.Source + m.Target
			}
			if p.Data.NextCursor == nil || page > 20 {
				break
			}
			cursor = *p.Data.NextCursor
		}
		if entries != tc.entries || replacements != tc.replacements {
			t.Errorf("%q: pages hold %d entries / %d replacements, want %d / %d", tc.find, entries, replacements, tc.entries, tc.replacements)
		}
		if first.Data.TotalEntries != entries || first.Data.TotalReplacements != replacements {
			t.Errorf("%q: totals %d / %d, pages sum to %d / %d", tc.find, first.Data.TotalEntries, first.Data.TotalReplacements, entries, replacements)
		}
	}
}
