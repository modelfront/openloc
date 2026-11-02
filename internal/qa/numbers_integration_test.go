package qa_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

// Every QA run, bulk confirm included, reads numbers with the project's languages (en → de): "1,000"
// is one thousand in the English source, "1.000" is one thousand in the German
// target, and "25.16" is not "2516".
func TestIntegrationQANumbers_ReadWithProjectLanguages(t *testing.T) {
	f := newQAScale(t, "number_consistency")
	ids := []int64{
		f.addSegment(t, 1, "1,000 items", "1 Artikel"),
		f.addSegment(t, 2, "Price: 25.16 EUR", "Preis: 2516 EUR"),
		f.addSegment(t, 3, "1,000 items", "1.000 Artikel"),
		f.addSegment(t, 4, "Total 1,234.56", "Summe 1.234,56"),
	}
	findings := []string{
		fmt.Sprintf("%d|number_added|Number(s) in target but not source: 1", ids[0]),
		fmt.Sprintf("%d|number_consistency|Missing number(s) in target: 1,000", ids[0]),
		fmt.Sprintf("%d|number_added|Number(s) in target but not source: 2516", ids[1]),
		fmt.Sprintf("%d|number_consistency|Missing number(s) in target: 25.16", ids[1]),
	}
	sort.Strings(findings)
	want := strings.Join(findings, "\n")

	// Bulk confirm needs drafts and approves them (the profile's checks are
	// warnings), so it runs last.
	runs := []struct {
		name string
		run  func()
	}{
		{"one segment", func() {
			for _, id := range ids {
				f.runOne(t, id)
			}
		}},
		{"whole job", func() { f.runJob(t) }},
		{"selection", func() {
			rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", f.jobID),
				map[string]any{"translation_ids": ids}, f.userID, f.tenantID)
			testutil.AssertStatus(t, rec, http.StatusOK)
		}},
		{"bulk confirm", func() {
			if _, err := f.pool.Exec(context.Background(),
				`UPDATE translations SET status = 'needs_review' WHERE id = ANY($1)`, ids); err != nil {
				t.Fatal(err)
			}
			rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/translations/confirm", f.jobID),
				map[string]any{"translation_ids": ids}, f.userID, f.tenantID)
			testutil.AssertStatus(t, rec, http.StatusOK)
		}},
	}
	for _, r := range runs {
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM qa_results`); err != nil {
			t.Fatal(err)
		}
		r.run()
		if got := strings.Join(f.findings(t), "\n"); got != want {
			t.Errorf("%s run:\ngot:\n%s\nwant:\n%s", r.name, got, want)
		}
	}
}

// postRule saves one rule of the fixture's profile and returns the response.
func (f qaScale) postRule(t *testing.T, rule map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", f.profileID), rule, f.userID, f.tenantID)
}

// The Numbers group through a real QA run: a changed time, date and
// conversion each get their own finding, a correct conversion none, and the
// number_format mode is read from the stored rule.
func TestIntegrationQANumbers_GroupChecksThroughQARuns(t *testing.T) {
	f := newQAScale(t, "number_consistency", "date_consistency", "time_consistency", "unit_conversion")
	testutil.AssertStatus(t, f.postRule(t, map[string]any{
		"check_type": "number_format", "enabled": true, "severity": "warning", "config": map[string]any{"mode": "require"},
	}), http.StatusOK)
	ids := []int64{
		f.addSegment(t, 1, "Check-in from 3:30 PM.", "Anreise ab 13:30 Uhr."),
		f.addSegment(t, 2, "Valid until March 14, 2026.", "Gültig bis 14.04.2026."),
		f.addSegment(t, 3, "Drive 5 miles", "Fahren Sie 5 km"),
		f.addSegment(t, 4, "Drive 5 miles", "Fahren Sie 8 km"),
		f.addSegment(t, 5, "Price: 25.16 EUR", "Preis: 25.16 EUR"),
	}
	findings := []string{
		fmt.Sprintf("%d|time_consistency|Time changed in target: 3:30 PM → 13:30 Uhr", ids[0]),
		fmt.Sprintf("%d|date_consistency|Date changed in target: March 14, 2026 → 14.04.2026", ids[1]),
		fmt.Sprintf("%d|unit_conversion|Converted value does not match: 5 miles → 5 km (about 8.0 km)", ids[2]),
		fmt.Sprintf("%d|number_format|Number(s) not in the target language's format: 25.16 (expected 25,16)", ids[4]),
	}
	sort.Strings(findings)
	want := strings.Join(findings, "\n")

	for _, id := range ids {
		f.runOne(t, id)
	}
	if got := strings.Join(f.findings(t), "\n"); got != want {
		t.Errorf("one-segment runs:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM qa_results`); err != nil {
		t.Fatal(err)
	}
	f.runJob(t)
	if got := strings.Join(f.findings(t), "\n"); got != want {
		t.Errorf("job run:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The API refuses a number_format mode it does not know, and keeps the one it
// does.
func TestIntegrationQANumbers_NumberFormatModeIsValidated(t *testing.T) {
	f := newQAScale(t)
	for _, mode := range []any{"strict", 1, ""} {
		rec := f.postRule(t, map[string]any{"check_type": "number_format", "enabled": true, "severity": "warning", "config": map[string]any{"mode": mode}})
		if mode == "" {
			testutil.AssertStatus(t, rec, http.StatusOK)
			continue
		}
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	}
	testutil.AssertStatus(t, f.postRule(t, map[string]any{"check_type": "number_format", "enabled": true, "severity": "warning", "config": map[string]any{"mode": "require"}}), http.StatusOK)
	var config string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT config::text FROM qa_rules WHERE profile_id = $1 AND check_type = 'number_format'`, f.profileID).Scan(&config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `"require"`) {
		t.Errorf("stored config = %s, want mode require", config)
	}
}

// The language separators of the number_format rule decide how numbers are
// read while the rule is on, in one-segment and job runs; while it is off they
// are stored but not used. They are validated, stored normalized and kept by
// export and import.
func TestIntegrationQANumbers_LanguageSeparators(t *testing.T) {
	f := newQAScale(t, "number_consistency")
	id := f.addSegment(t, 1, "1000 items", "1,000 Artikel")
	f.runOne(t, id)
	before := f.findings(t)
	if len(before) == 0 {
		t.Fatal("de reads 1,000 as one: want a finding before the override")
	}

	languages := []map[string]string{{"language": "DE", "decimal": ".", "group": ","}}
	testutil.AssertStatus(t, f.postRule(t, map[string]any{"check_type": "number_format", "enabled": false, "severity": "warning",
		"config": map[string]any{"mode": "allow", "languages": []map[string]string{{"language": "de", "decimal": ".", "group": "."}}}}),
		http.StatusBadRequest)
	testutil.AssertStatus(t, f.postRule(t, map[string]any{"check_type": "number_format", "enabled": false, "severity": "warning",
		"config": map[string]any{"mode": "allow", "languages": languages}}),
		http.StatusOK)
	var config string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT config::text FROM qa_rules WHERE profile_id = $1 AND check_type = 'number_format'`, f.profileID).Scan(&config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `"language": "de"`) {
		t.Errorf("stored config = %s, want the language normalized to de", config)
	}

	f.runOne(t, id)
	if got := strings.Join(f.findings(t), "\n"); got != strings.Join(before, "\n") {
		t.Errorf("number_format off: the stored separators must not be used, got:\n%s\nwant:\n%s", got, strings.Join(before, "\n"))
	}

	testutil.AssertStatus(t, f.postRule(t, map[string]any{"check_type": "number_format", "enabled": true, "severity": "warning",
		"config": map[string]any{"mode": "allow", "languages": languages}}),
		http.StatusOK)
	f.runOne(t, id)
	if got := f.findings(t); len(got) != 0 {
		t.Errorf("one-segment run, de written the English way: want no findings, got %v", got)
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM qa_results`); err != nil {
		t.Fatal(err)
	}
	f.runJob(t)
	if got := f.findings(t); len(got) != 0 {
		t.Errorf("job run, de written the English way: want no findings, got %v", got)
	}

	rec := testutil.DoRequest(t, f.router, "GET", fmt.Sprintf("/api/qa-profiles/%d/export", f.profileID), nil, f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, f.router, "POST", "/api/qa-profiles/import", json.RawMessage(rec.Body.Bytes()), f.userID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var imported struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &imported)
	if err := f.pool.QueryRow(context.Background(),
		`SELECT config::text FROM qa_rules WHERE profile_id = $1 AND check_type = 'number_format'`, imported.Data.ID).Scan(&config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `"language": "de"`) || !strings.Contains(config, `"decimal": "."`) {
		t.Errorf("imported config = %s, want the de separators kept", config)
	}
}
