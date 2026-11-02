package translationmemory_test

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The Resources panel explains a docked match from the penalties the server
// lists on it, so each match must carry exactly the deductions that took its
// percent below the text similarity — with the languages for a variant — and
// a clean match must carry none.
func TestIntegrationTMMatch_ListsPenalties(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "TM-Penalty-Breakdown")
	user := testutil.SeedUser(t, q, tenant.ID, "u@breakdown.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en-us", []string{"fr-fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr-fr")
	legacy := testutil.SeedTM(t, q, tenant.ID, "Legacy TM")
	main := testutil.SeedTM(t, q, tenant.ID, "Main TM")
	testutil.SeedTMAssignmentPenalty(t, q, project.SettingsProfileID, legacy.ID, "read", 0, 5)
	testutil.SeedTMAssignmentPenalty(t, q, project.SettingsProfileID, main.ID, "read", 1, 0)
	for _, e := range []struct {
		tmID         int64
		src, srcText string
		tgt, tgtText string
	}{
		{legacy.ID, "en", "Hello world", "fr-fr", "variant"},
		{legacy.ID, "en-us", "Hello world!", "fr-fr", "near exact"},
		{main.ID, "en-us", "Hello world", "fr-fr", "clean"},
	} {
		testutil.SeedTMEntry(t, q, e.tmID, e.src, e.tgt, e.srcText, e.tgtText)
	}

	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape("Hello world"))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	type penalty struct {
		Kind            string `json:"kind"`
		Percent         int    `json:"percent"`
		TMLanguage      string `json:"tm_language"`
		ProjectLanguage string `json:"project_language"`
	}
	var resp struct {
		Matches []struct {
			TargetText string    `json:"target_text"`
			Percent    int       `json:"percent"`
			Penalties  []penalty `json:"penalties"`
		} `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	want := map[string]struct {
		percent   int
		penalties []penalty
	}{
		"variant": {94, []penalty{
			{Kind: "source_variant", Percent: 1, TMLanguage: "en", ProjectLanguage: "en-us"},
			{Kind: "tm", Percent: 5},
		}},
		"near exact": {94, []penalty{{Kind: "near_exact", Percent: 1}, {Kind: "tm", Percent: 5}}},
		"clean":      {100, nil},
	}
	if len(resp.Matches) != len(want) {
		t.Fatalf("got %d matches, want %d: %+v", len(resp.Matches), len(want), resp.Matches)
	}
	for _, m := range resp.Matches {
		w, ok := want[m.TargetText]
		if !ok {
			t.Errorf("unexpected match %q", m.TargetText)
			continue
		}
		if m.Percent != w.percent {
			t.Errorf("%q: percent %d, want %d", m.TargetText, m.Percent, w.percent)
		}
		if !reflect.DeepEqual(m.Penalties, w.penalties) {
			t.Errorf("%q: penalties %+v, want %+v", m.TargetText, m.Penalties, w.penalties)
		}
	}
}

// A match is exact only when its source is the segment's own and nothing was
// deducted — the matches pre-translation approves. The panel approves a match
// on this flag, so a hit that merely rounds to 100% must not carry it.
func TestIntegrationTMMatch_MarksExact(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "TM-Exact-Flag")
	user := testutil.SeedUser(t, q, tenant.ID, "u@exact.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")
	docked := testutil.SeedTM(t, q, tenant.ID, "Docked TM")
	main := testutil.SeedTM(t, q, tenant.ID, "Main TM")
	testutil.SeedTMAssignmentPenalty(t, q, project.SettingsProfileID, main.ID, "read", 0, 0)
	testutil.SeedTMAssignmentPenalty(t, q, project.SettingsProfileID, docked.ID, "read", 1, 5)
	// Long enough that one changed character still rounds to 100%.
	const query = "The quarterly report covers revenue, operating costs, staffing changes, " +
		"regional performance, customer retention, supplier contracts, capital spending, " +
		"and the outlook for the next two quarters, with notes on risks and open questions for the board."
	oneOff := strings.Replace(query, "board", "boart", 1)
	for _, e := range []struct {
		tmID         int64
		src, tgtText string
	}{
		{main.ID, query, "clean"},
		{main.ID, oneOff, "one character off"},
		{docked.ID, query, "docked"},
	} {
		testutil.SeedTMEntry(t, q, e.tmID, "en", "fr", e.src, e.tgtText)
	}

	path := fmt.Sprintf("/api/jobs/%d/tm-matches?source_text=%s", job.ID, url.QueryEscape(query))
	rec := testutil.DoRequest(t, router, "GET", path, nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Matches []struct {
			TargetText string `json:"target_text"`
			Percent    int    `json:"percent"`
			Exact      bool   `json:"exact"`
		} `json:"matches"`
	}
	testutil.DecodeJSON(t, rec, &resp)

	want := map[string]struct {
		percent int
		exact   bool
	}{
		"clean":             {100, true},
		"one character off": {100, false},
		"docked":            {95, false},
	}
	if len(resp.Matches) != len(want) {
		t.Fatalf("got %d matches, want %d: %+v", len(resp.Matches), len(want), resp.Matches)
	}
	for _, m := range resp.Matches {
		w, ok := want[m.TargetText]
		if !ok {
			t.Errorf("unexpected match %q", m.TargetText)
			continue
		}
		if m.Percent != w.percent || m.Exact != w.exact {
			t.Errorf("%q: percent %d exact %v, want %d %v", m.TargetText, m.Percent, m.Exact, w.percent, w.exact)
		}
	}
}
