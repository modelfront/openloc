package mcp_test

import (
	"context"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// attachQAProfile gives projectA a QA profile with number_consistency at
// severity=error, so a target that adds a number produces a blocking finding.
func attachQAProfile(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	prof, err := f.q.CreateQAProfile(ctx, db.CreateQAProfileParams{
		TenantID: f.tenantA.ID, Name: "MCP QA",
	})
	if err != nil {
		t.Fatalf("create qa profile: %v", err)
	}
	if _, err := f.q.UpsertQARule(ctx, db.UpsertQARuleParams{
		ProfileID: prof.ID, CheckType: "number_consistency", Enabled: true, Severity: "error", Config: []byte("{}"),
	}); err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	if _, err := f.q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: f.projectA.SettingsProfileID, FuzzyThreshold: 0.75, QaProfileID: &prof.ID,
	}); err != nil {
		t.Fatalf("assign qa profile: %v", err)
	}
}

type mcpFinding struct {
	TranslationID int64  `json:"translation_id"`
	CheckType     string `json:"check_type"`
	Severity      string `json:"severity"`
	Ignored       bool   `json:"ignored"`
	IgnoredBy     string `json:"ignored_by"`
}

type mcpSummary struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
	Ignored int `json:"ignored"`
}

func findMCP(rows []mcpFinding, checkType string) *mcpFinding {
	for i := range rows {
		if rows[i].CheckType == checkType {
			return &rows[i]
		}
	}
	return nil
}

// TestIntegrationMCPQATools drives the two QA write tools end to end through a
// real MCP session: run_qa produces findings, ignore_qa_finding flags one, the
// report reflects it, a second run keeps it, and unignore restores it.
func TestIntegrationMCPQATools(t *testing.T) {
	f := newFixture(t)
	attachQAProfile(t, f)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	// A source WITH a number: dropping it from the target fires
	// number_consistency at the rule's severity (error). A target-only number
	// is the softer target-side check, always a warning.
	job := seedJobWithSegments(t, f.q, f.projectA.ID, "numbers.txt", "de", []string{"Hello 42 world"})
	translationID, _ := firstTranslation(t, s, job.ID)
	target := "Hallo Welt"
	if _, err := f.q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: translationID, HumanText: &target}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	const check = "number_consistency"

	var run struct {
		Findings int        `json:"findings"`
		NoRules  bool       `json:"no_rules"`
		Summary  mcpSummary `json:"summary"`
	}
	t.Run("run_qa writes findings", func(t *testing.T) {
		if isErr, text := callTool(t, s, "openloc_run_qa", map[string]any{"job_id": job.ID}, &run); isErr {
			t.Fatalf("run_qa: %s", text)
		}
		if run.Findings == 0 || run.Summary.Error == 0 {
			t.Fatalf("expected at least one error finding, got %+v", run)
		}
		rows, err := f.q.ListQAResultsForTranslation(ctx, translationID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(rows) == 0 || rows[0].CheckType != check || rows[0].Severity != "error" {
			t.Fatalf("%s error not persisted: %+v", check, rows)
		}
	})

	var list struct {
		Findings []mcpFinding `json:"findings"`
	}
	t.Run("ignore_qa_finding flags the row", func(t *testing.T) {
		if isErr, text := callTool(t, s, "openloc_ignore_qa_finding", map[string]any{
			"translation_id": translationID, "check_type": check,
		}, &list); isErr {
			t.Fatalf("ignore: %s", text)
		}
		fnd := findMCP(list.Findings, check)
		if fnd == nil || !fnd.Ignored || fnd.IgnoredBy == "" {
			t.Fatalf("finding not flagged with author: %+v", list.Findings)
		}
		rows, err := f.q.ListQAResultsForTranslation(ctx, translationID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if !rows[0].Ignored || rows[0].IgnoredBy == nil || *rows[0].IgnoredBy != f.ownerA.ID {
			t.Fatalf("DB row not ignored by the caller: %+v", rows[0])
		}
	})

	var rep struct {
		Summary mcpSummary   `json:"summary"`
		Issues  []mcpFinding `json:"issues"`
	}
	t.Run("get_qa_report flags and re-buckets it", func(t *testing.T) {
		if isErr, text := callTool(t, s, "openloc_get_qa_report", map[string]any{"job_id": job.ID}, &rep); isErr {
			t.Fatalf("report: %s", text)
		}
		if rep.Summary.Ignored != 1 || rep.Summary.Error != run.Summary.Error-1 {
			t.Errorf("summary: want ignored=1, error=%d, got %+v", run.Summary.Error-1, rep.Summary)
		}
		fnd := findMCP(rep.Issues, check)
		if fnd == nil || !fnd.Ignored {
			t.Errorf("report must list the ignored finding, flagged: %+v", rep.Issues)
		}
	})

	t.Run("run_qa keeps the ignore", func(t *testing.T) {
		if isErr, text := callTool(t, s, "openloc_run_qa", map[string]any{"job_id": job.ID}, &run); isErr {
			t.Fatalf("run_qa: %s", text)
		}
		if run.Summary.Ignored != 1 {
			t.Fatalf("second run dropped the ignore: %+v", run.Summary)
		}
	})

	t.Run("unignore restores it", func(t *testing.T) {
		// Fresh struct: ignored_by/ignored_at are omitempty on the wire, so decoding
		// into the reused `list` would keep their stale values.
		var after struct {
			Findings []mcpFinding `json:"findings"`
		}
		if isErr, text := callTool(t, s, "openloc_ignore_qa_finding", map[string]any{
			"translation_id": translationID, "check_type": check, "unignore": true,
		}, &after); isErr {
			t.Fatalf("unignore: %s", text)
		}
		fnd := findMCP(after.Findings, check)
		if fnd == nil || fnd.Ignored || fnd.IgnoredBy != "" {
			t.Fatalf("un-ignore must clear flag and author: %+v", after.Findings)
		}
	})

	t.Run("unknown check_type is a tool error, not a 500", func(t *testing.T) {
		isErr, text := callTool(t, s, "openloc_ignore_qa_finding", map[string]any{
			"translation_id": translationID, "check_type": "punctuation",
		}, nil)
		if !isErr {
			t.Fatal("ignoring a finding that does not exist succeeded")
		}
		if !strings.Contains(text, "no current") {
			t.Errorf("expected an actionable message, got %q", text)
		}
	})

	t.Run("run_qa without a QA profile explains itself", func(t *testing.T) {
		bare := testutil.SeedProject(t, f.q, f.tenantA.ID, "No QA Project", "en", []string{"de"})
		job := seedJobWithSegments(t, f.q, bare.ID, "bare.txt", "de", []string{"Only line"})
		isErr, text := callTool(t, s, "openloc_run_qa", map[string]any{"job_id": job.ID}, nil)
		if !isErr {
			t.Fatal("run_qa succeeded on a project with no QA profile")
		}
		if !strings.Contains(text, "QA profile") {
			t.Errorf("expected the message to name the missing QA profile, got %q", text)
		}
	})
}
