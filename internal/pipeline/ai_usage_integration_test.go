package pipeline_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/llmdebug"
	"openloc/internal/metrics"
	"openloc/internal/pipeline"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

// Token counts the fake provider reports for every call.
const (
	stubPromptTokens     = 120
	stubCompletionTokens = 30
)

// usageStub is an OpenAI-shaped provider that answers each batched segment with
// verdict(id) and reports a fixed usage block, so the test can follow tokens
// from the provider reply to ai_usage_events.
func usageStub(t *testing.T, verdict func(id string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		var segs []struct {
			ID json.RawMessage `json:"id"`
		}
		for _, m := range req.Messages {
			if m.Role == "user" {
				if err := json.Unmarshal([]byte(m.Content), &segs); err != nil {
					t.Errorf("user message is not a segment array: %v", err)
				}
			}
		}
		objects := make([]string, len(segs))
		for i, s := range segs {
			objects[i] = `{"id":` + string(s.ID) + `,` + verdict(string(s.ID)) + `}`
		}
		content := `{"results":[` + strings.Join(objects, ",") + `]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": stubPromptTokens, "completion_tokens": stubCompletionTokens},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// aiUsageEnv is one tenant with an OpenAI connection pointing at a stub and a
// service wired the way production wires it (ConfigureAI).
type aiUsageEnv struct {
	pool    *pgxpool.Pool
	q       db.Querier
	svc     *pipeline.Service
	tenant  db.Tenant
	project db.Project
	file    db.File
	tr      db.Translation
	tokenID int64
}

func newAIUsageEnv(t *testing.T, srvURL string) *aiUsageEnv {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.ConfigureAI(testutil.TestAISecretsKey, nil); err != nil {
		t.Fatalf("ConfigureAI: %v", err)
	}
	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	key, err := enc.Encrypt("sk-test")
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}

	tenant := testutil.SeedTenant(t, q, "UsageCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Usage Project", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "usage.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusNeedsReview)
	tok := testutil.SeedAIVendorTokenBase(t, q, tenant.ID, "openai", "Stub", key, srvURL)

	return &aiUsageEnv{pool: pool, q: q, svc: svc, tenant: tenant, project: project, file: file, tr: tr, tokenID: tok.ID}
}

// engine creates an AI engine of the given purpose whose only row routes en→fr
// through a prompt with the given output flags.
func (e *aiUsageEnv) engine(t *testing.T, name, purpose string, outStatus bool) db.AiEngine {
	t.Helper()
	ctx := context.Background()
	prompt, err := e.q.CreateAIPrompt(ctx, db.CreateAIPromptParams{
		TenantID: e.tenant.ID, Name: name + " prompt", PromptText: "Do the job.", OutStatus: outStatus,
	})
	if err != nil {
		t.Fatalf("CreateAIPrompt: %v", err)
	}
	engine, err := e.q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: e.tenant.ID, Name: name, Purpose: purpose})
	if err != nil {
		t.Fatalf("CreateAIEngine: %v", err)
	}
	if _, err := e.q.CreateAIEngineRow(ctx, db.CreateAIEngineRowParams{
		EngineID: engine.ID, SourceLang: "en", TargetLangs: []string{"fr"},
		SystemPromptID: &prompt.ID, TokenID: &e.tokenID, Model: "gpt-4o-mini", Settings: []byte("{}"),
	}); err != nil {
		t.Fatalf("CreateAIEngineRow: %v", err)
	}
	return engine
}

type usageRow struct {
	engineID      *int64
	engineName    string
	purpose       string
	provider      string
	projectID     *int64
	jobID         *int64
	input, output int64
}

func (e *aiUsageEnv) usageRows(t *testing.T) []usageRow {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT ai_engine_id, ai_engine_name, purpose, provider, project_id, job_id, input_tokens, output_tokens
		FROM ai_usage_events WHERE tenant_id = $1 ORDER BY id`, e.tenant.ID)
	if err != nil {
		t.Fatalf("query ai_usage_events: %v", err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		var r usageRow
		if err := rows.Scan(&r.engineID, &r.engineName, &r.purpose, &r.provider, &r.projectID, &r.jobID, &r.input, &r.output); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// tokenTotal reads the tenant's tokens back through the ai_token_usage measure,
// which is what the dashboard chart runs.
func (e *aiUsageEnv) tokenTotal(t *testing.T, tenantID int64) int64 {
	t.Helper()
	res, err := metrics.Run(context.Background(), e.pool, tenantID, metrics.Request{
		Measure: "ai_token_usage", From: time.Now().AddDate(0, 0, -1), To: time.Now().AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("run ai_token_usage: %v", err)
	}
	var total int64
	for _, row := range res.Rows {
		switch v := row["total_tokens"].(type) {
		case int64:
			total += v
		case int32:
			total += int64(v)
		}
	}
	return total
}

// idText prints a nullable id for a failure message.
func idText(id *int64) string {
	if id == nil {
		return "NULL"
	}
	return strconv.FormatInt(*id, 10)
}

func assertUsage(t *testing.T, got []usageRow, engine db.AiEngine, projectID int64, jobID *int64) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want 1 ai_usage_events row, got %d", len(got))
	}
	r := got[0]
	if r.engineID == nil || *r.engineID != engine.ID || r.engineName != engine.Name || r.purpose != engine.Purpose {
		t.Errorf("attribution: engine %s %q purpose %q, want %d %q %q", idText(r.engineID), r.engineName, r.purpose, engine.ID, engine.Name, engine.Purpose)
	}
	if r.provider != "openai" {
		t.Errorf("provider = %q, want openai", r.provider)
	}
	if r.projectID == nil || *r.projectID != projectID {
		t.Errorf("project_id = %s, want %d", idText(r.projectID), projectID)
	}
	if (jobID == nil) != (r.jobID == nil) || (jobID != nil && *r.jobID != *jobID) {
		t.Errorf("job_id = %s, want %s", idText(r.jobID), idText(jobID))
	}
	if r.input != stubPromptTokens || r.output != stubCompletionTokens {
		t.Errorf("tokens = %d/%d, want %d/%d", r.input, r.output, stubPromptTokens, stubCompletionTokens)
	}
}

// AI-as-MT through the production wiring records one usage row per provider
// call, attributed to the tenant, engine, project and job, and the dashboard
// measure reads it back for that tenant only. The job rides the debug meta, as
// the worker sets it before TranslateFile.
func TestIntegrationAIUsage_RecordedForAIMT(t *testing.T) {
	srv := usageStub(t, func(string) string { return `"target":"Bonjour le monde"` })
	e := newAIUsageEnv(t, srv.URL)
	engine := e.engine(t, "GPT translator", "mt", false)
	testutil.SeedMTRouterForProfile(t, e.q, e.tenant.ID, e.project.SettingsProfileID, nil, &engine.ID)
	job := testutil.SeedJob(t, e.q, e.file.ID, "fr")
	ctx := llmdebug.WithMeta(context.Background(), llmdebug.Meta{JobID: job.ID})

	if _, err := e.svc.TranslateFile(ctx, e.tenant.ID, e.file.ID, "en", "fr", nil,
		pipeline.TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	assertUsage(t, e.usageRows(t), engine, e.project.ID, &job.ID)
	if got := e.tokenTotal(t, e.tenant.ID); got != stubPromptTokens+stubCompletionTokens {
		t.Errorf("ai_token_usage total = %d, want %d", got, stubPromptTokens+stubCompletionTokens)
	}
	other := testutil.SeedTenant(t, e.q, "OtherCorp")
	if got := e.tokenTotal(t, other.ID); got != 0 {
		t.Errorf("another tenant sees %d tokens, want 0", got)
	}
}

// The QE+APE step records its provider calls too, attributed to the step's
// engine and job. A segment the engine approved as it was counts in
// ai_engine_usage, like one it post-edited: both were sent to it.
func TestIntegrationAIUsage_RecordedForQEStep(t *testing.T) {
	srv := usageStub(t, func(string) string { return `"approved":true,"score":95` })
	e := newAIUsageEnv(t, srv.URL)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`UPDATE translations SET active_text = 'Bonjour le monde', origin = 'mt' WHERE id = $1`, e.tr.ID); err != nil {
		t.Fatalf("seed MT text: %v", err)
	}
	engine := e.engine(t, "GPT reviewer", "qe_ape", true)
	job := testutil.SeedJob(t, e.q, e.file.ID, "fr")
	routerID := testutil.SeedRouterForEngine(t, e.q, e.tenant.ID, engine.ID)
	testutil.SeedAIJobStepWithRouter(t, e.q, e.project.SettingsProfileID, job.ID, routerID)

	if _, err := e.svc.RunAIStep(ctx, e.tenant.ID, job.ID, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}

	assertUsage(t, e.usageRows(t), engine, e.project.ID, &job.ID)

	var outcome *string
	var historyEngine *int64
	if err := e.pool.QueryRow(ctx, `
		SELECT t.predict_outcome::text, th.ai_engine_id
		FROM translations t JOIN translation_history th ON th.translation_id = t.id AND th.kind = 'auto_approved'
		WHERE t.id = $1`, e.tr.ID).Scan(&outcome, &historyEngine); err != nil {
		t.Fatalf("read auto_approved history: %v", err)
	}
	if outcome == nil || *outcome != "approved" {
		t.Fatalf("predict_outcome = %v, want approved", outcome)
	}
	if historyEngine == nil || *historyEngine != engine.ID {
		t.Errorf("auto_approved history ai_engine_id = %v, want %d", historyEngine, engine.ID)
	}

	res, err := metrics.Run(ctx, e.pool, e.tenant.ID, metrics.Request{
		Measure: "ai_engine_usage", Dimensions: []string{"ai_engine"},
		From: time.Now().AddDate(0, 0, -1), To: time.Now().AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("run ai_engine_usage: %v", err)
	}
	var segments int64
	for _, row := range res.Rows {
		if row["ai_engine"] == engine.Name {
			segments, _ = row["segments"].(int64)
		}
	}
	if segments != 1 {
		t.Errorf("ai_engine_usage for %q = %d segments, want the 1 it approved: %v", engine.Name, segments, res.Rows)
	}
}
