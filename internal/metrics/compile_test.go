package metrics

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	from = time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	to   = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
)

func mustCompile(t *testing.T, req Request) *compiled {
	t.Helper()
	c, err := compile(42, req)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

// The user's motivating example, end to end: MT engine usage for German by
// month. Golden SQL pins the two-layer dedup shape and the placement of every
// caller value as a bind parameter.
func TestCompileGoldenMTEngineUsage(t *testing.T) {
	c := mustCompile(t, Request{
		Measure:    "mt_engine_usage",
		Dimensions: []string{"mt_engine", "month"},
		Filters:    map[string]string{"target_language": "DE"},
		From:       from, To: to,
	})

	want := `SELECT "mt_engine", "month", COUNT(*) AS segments, COALESCE(SUM(word_count), 0) AS words, COALESCE(SUM(char_count), 0) AS characters
FROM (
  SELECT DISTINCT t.id AS _unit, length(s.source_text) AS char_count, s.word_count AS word_count, me.name AS "mt_engine", date_trunc('month', th.created_at)::date AS "month"
  FROM translation_history th
JOIN translations t ON t.id = th.translation_id
JOIN segments s ON s.id = t.segment_id
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
LEFT JOIN users u ON u.id = th.created_by
LEFT JOIN mt_engines me ON me.id = th.mt_engine_id
LEFT JOIN label_definitions ld_c ON ld_c.tenant_id = p.tenant_id
                               AND ld_c.system_key = 'customer' AND ld_c.deleted_at IS NULL
LEFT JOIN project_labels pl_c ON pl_c.project_id = p.id AND pl_c.label_definition_id = ld_c.id
LEFT JOIN label_values c ON c.id = pl_c.label_value_id AND c.deleted_at IS NULL
LEFT JOIN ai_engines ais ON ais.id = th.ai_engine_id
LEFT JOIN job_step_assignments jsa ON jsa.id = th.job_step_assignment_id
LEFT JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
  WHERE p.tenant_id = $1 AND th.tenant_id = $1 AND p.deleted_at IS NULL AND th.created_at >= $2 AND th.created_at < $3 AND th.kind = 'mt' AND th.mt_engine_id IS NOT NULL AND LOWER(t.target_language) = $4
) sub
GROUP BY "mt_engine", "month"
ORDER BY "month" ASC
LIMIT 501`
	if c.sql != want {
		t.Errorf("sql mismatch:\n--- got ---\n%s\n--- want ---\n%s", c.sql, want)
	}
	// Language filter normalized (DE → de) and bound, never inlined.
	if len(c.args) != 4 || c.args[0] != int64(42) || c.args[3] != "de" {
		t.Errorf("args = %v", c.args)
	}
	wantCols := []string{"mt_engine", "month", "segments", "words", "characters"}
	if strings.Join(c.columns, ",") != strings.Join(wantCols, ",") {
		t.Errorf("columns = %v", c.columns)
	}
}

// Single-layer shape (no distinctUnit): qa_issues groups raw rows.
func TestCompileGoldenQAIssues(t *testing.T) {
	c := mustCompile(t, Request{
		Measure:    "qa_issues",
		Dimensions: []string{"severity"},
		From:       from, To: to,
	})
	if strings.Contains(c.sql, "SELECT DISTINCT") {
		t.Errorf("qa_issues must not use the dedup subquery:\n%s", c.sql)
	}
	if !strings.Contains(c.sql, `qr.severity AS "severity"`) ||
		!strings.Contains(c.sql, "GROUP BY qr.severity") ||
		!strings.Contains(c.sql, "ORDER BY issues DESC") {
		t.Errorf("unexpected shape:\n%s", c.sql)
	}
}

// Every registered measure must carry the tenant pin and terminate its join
// graph in projects. This is the whole security model; a measure that fails
// here must not exist.
func TestCompileEveryMeasureIsTenantPinned(t *testing.T) {
	for name := range measures {
		c, err := compile(7, Request{Measure: name, From: from, To: to})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(c.sql, "p.tenant_id = $1") {
			t.Errorf("%s: missing tenant pin:\n%s", name, c.sql)
		}
		if !strings.Contains(c.sql, "JOIN projects p ON") {
			t.Errorf("%s: join graph does not pass through projects:\n%s", name, c.sql)
		}
		if c.args[0] != int64(7) {
			t.Errorf("%s: $1 is not the tenant id: %v", name, c.args)
		}
	}
}

// Caller-supplied strings must never appear in SQL text — only in args.
// These are the actual injection shapes a hostile prompt would produce.
func TestCompileInjectionAttemptsFailClosedOrBind(t *testing.T) {
	// Hostile measure / dimension / filter-key names: rejected outright.
	hostile := []Request{
		{Measure: "words_processed; DROP TABLE projects; --", From: from, To: to},
		{Measure: "words_processed", Dimensions: []string{"p.tenant_id = 0 OR user"}, From: from, To: to},
		{Measure: "words_processed", Filters: map[string]string{"1=1) OR (1=1": "x"}, From: from, To: to},
		{Measure: "words_processed", Filters: map[string]string{"tenant_id": "1"}, From: from, To: to},
	}
	for i, req := range hostile {
		if _, err := compile(1, req); !errors.Is(err, ErrValidation) {
			t.Errorf("case %d: want ErrValidation, got %v", i, err)
		}
	}

	// Hostile filter VALUE: legal request, value must bind, not splice.
	evil := `de' OR '1'='1`
	c := mustCompile(t, Request{
		Measure: "words_processed",
		Filters: map[string]string{"project": evil},
		From:    from, To: to,
	})
	if strings.Contains(c.sql, "1'='1") {
		t.Fatalf("filter value leaked into SQL text:\n%s", c.sql)
	}
	found := false
	for _, a := range c.args {
		if a == evil {
			found = true
		}
	}
	if !found {
		t.Errorf("filter value missing from args: %v", c.args)
	}
}

func TestCompileValidationFailsClosed(t *testing.T) {
	cases := map[string]Request{
		"unknown measure":        {Measure: "revenue", From: from, To: to},
		"foreign dimension":      {Measure: "words_processed", Dimensions: []string{"check_type"}, From: from, To: to},
		"repeated dimension":     {Measure: "words_processed", Dimensions: []string{"user", "user"}, From: from, To: to},
		"too many dimensions":    {Measure: "words_processed", Dimensions: []string{"user", "project", "customer", "month"}, From: from, To: to},
		"missing range":          {Measure: "words_processed"},
		"inverted range":         {Measure: "words_processed", From: to, To: from},
		"range too wide":         {Measure: "words_processed", From: from.AddDate(-2, 0, 0), To: to},
		"empty filter value":     {Measure: "words_processed", Filters: map[string]string{"user": "  "}, From: from, To: to},
		"non-numeric id filter":  {Measure: "words_processed", Filters: map[string]string{"project_id": "abc"}, From: from, To: to},
		"time grain as filter":   {Measure: "words_processed", Filters: map[string]string{"month": "2026-06"}, From: from, To: to},
		"filter foreign measure": {Measure: "qa_issues", Filters: map[string]string{"mt_engine": "DeepL"}, From: from, To: to},
	}
	for name, req := range cases {
		if _, err := compile(1, req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
}

// Categorical-only queries order by the first value column so "top N" reads
// top-first; presence of any time grain switches to chronological.
func TestCompileOrdering(t *testing.T) {
	cat := mustCompile(t, Request{Measure: "mt_engine_usage", Dimensions: []string{"mt_engine"}, From: from, To: to})
	if !strings.Contains(cat.sql, "ORDER BY segments DESC") {
		t.Errorf("categorical: %s", cat.sql)
	}
	series := mustCompile(t, Request{Measure: "mt_engine_usage", Dimensions: []string{"mt_engine", "week"}, From: from, To: to})
	if !strings.Contains(series.sql, `ORDER BY "week" ASC`) {
		t.Errorf("time series: %s", series.sql)
	}
}

// project_id filters bind as int64 — the one numeric filter path.
func TestCompileNumericFilterBindsInt(t *testing.T) {
	c := mustCompile(t, Request{
		Measure: "qa_issues",
		Filters: map[string]string{"project_id": "12"},
		From:    from, To: to,
	})
	if !strings.Contains(c.sql, "p.id = $4") {
		t.Errorf("sql: %s", c.sql)
	}
	if c.args[3] != int64(12) {
		t.Errorf("args: %v", c.args)
	}
}

func TestRangeFromLastDays(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 30, 0, 0, time.UTC)
	f, to, err := RangeFromLastDays(60, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if to != time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC) {
		t.Errorf("to = %v (today's events must be inside the window)", to)
	}
	if f != to.AddDate(0, 0, -60) {
		t.Errorf("from = %v", f)
	}
	for _, bad := range []int{0, -1, 366} {
		if _, _, err := RangeFromLastDays(bad, now); !errors.Is(err, ErrValidation) {
			t.Errorf("days=%d: want ErrValidation, got %v", bad, err)
		}
	}
	// The resolved range must be accepted by the compiler (365 + the "end of
	// today" extension must not trip the MaxRangeDays=366 cap).
	f2, to2, _ := RangeFromLastDays(365, now)
	if err := Validate(Request{Measure: "qa_issues", From: f2, To: to2}); err != nil {
		t.Errorf("365-day dashboard range rejected by compiler: %v", err)
	}
}
